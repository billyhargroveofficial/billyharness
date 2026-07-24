package jobservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const (
	defaultRetryInitialBackoff = 250 * time.Millisecond
	defaultRetryMaxBackoff     = 30 * time.Second
)

var (
	ErrClosed          = errors.New("job service is shut down")
	ErrNotStartable    = errors.New("job is not startable")
	ErrNotControllable = errors.New("job is not controllable")
	ErrPauseFailed     = errors.New("job did not reach the paused state")
)

// StepRunner is the jobruntime.Runner surface needed by Manager. Keeping the
// boundary narrow makes the service scheduler independently testable while a
// *jobruntime.Runner satisfies it directly.
type StepRunner interface {
	Step(context.Context, string) (jobs.JobState, bool, error)
	RequestCancel(context.Context, string) (jobs.JobState, error)
	RequestPause(context.Context, string) (jobs.JobState, error)
	RequestResume(context.Context, string) (jobs.JobState, error)
}

// StepErrorClassifier is an optional capability of a StepRunner. Manager
// retries only errors explicitly classified by the runner, and always reloads
// durable state before doing so. This keeps jobservice independent of runtime-
// specific error sentinels while avoiding blind retries for test/custom
// runners which do not implement the contract.
type StepErrorClassifier interface {
	RetryableStepError(error) bool
}

type dueExpirer interface {
	ExpireDue(context.Context, string) (jobs.JobState, bool, error)
}

type retryDelayError interface {
	RetryDelay() time.Duration
}

type Option func(*options) error

type options struct {
	retryInitialBackoff time.Duration
	retryMaxBackoff     time.Duration
}

// WithRetryBackoff overrides the cancellable exponential retry window. It is
// primarily useful for tests and tightly controlled embedded deployments.
func WithRetryBackoff(initial, maximum time.Duration) Option {
	return func(options *options) error {
		if initial <= 0 || maximum <= 0 || maximum < initial {
			return errors.New("job service retry backoff requires 0 < initial <= maximum")
		}
		options.retryInitialBackoff = initial
		options.retryMaxBackoff = maximum
		return nil
	}
}

// View combines canonical durable state with process-local scheduler state.
// Active and LastError are observations, not durable job inputs.
type View struct {
	State     jobs.JobState `json:"state"`
	Active    bool          `json:"active"`
	LastError string        `json:"last_error,omitempty"`
}

// Summary is the list representation of View.
type Summary struct {
	Job       jobstore.JobSummary `json:"job"`
	Active    bool                `json:"active"`
	LastError string              `json:"last_error,omitempty"`
}

type runEntry struct {
	jobID string
	done  chan struct{}
	wake  chan struct{}
	state jobs.JobState

	startupDone chan struct{}
	startupErr  error

	pauseRequested bool
	pauseSucceeded bool
	exitError      string
}

// Manager owns a server-lifetime context and one background Step loop per
// admitted job. The supplied Store remains owned by the caller and is not
// closed by Shutdown.
type Manager struct {
	store  jobstore.Store
	runner StepRunner

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	closed     bool
	active     map[string]*runEntry
	lastErrors map[string]string

	retryInitialBackoff time.Duration
	retryMaxBackoff     time.Duration
	retryClassifier     StepErrorClassifier
	wg                  sync.WaitGroup

	shutdownOnce sync.Once
	stopped      chan struct{}
}

func New(store jobstore.Store, runner StepRunner, opts ...Option) (*Manager, error) {
	if store == nil {
		return nil, errors.New("job store is required")
	}
	if runner == nil {
		return nil, errors.New("job runner is required")
	}
	resolved := options{
		retryInitialBackoff: defaultRetryInitialBackoff,
		retryMaxBackoff:     defaultRetryMaxBackoff,
	}
	for _, option := range opts {
		if option == nil {
			continue
		}
		if err := option(&resolved); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	classifier, _ := runner.(StepErrorClassifier)
	return &Manager{
		store:               store,
		runner:              runner,
		ctx:                 ctx,
		cancel:              cancel,
		active:              make(map[string]*runEntry),
		lastErrors:          make(map[string]string),
		retryInitialBackoff: resolved.retryInitialBackoff,
		retryMaxBackoff:     resolved.retryMaxBackoff,
		retryClassifier:     classifier,
		stopped:             make(chan struct{}),
	}, nil
}

// Recover resumes jobs which were durably RUNNING and re-arms timers for jobs
// which were durably WAITING. QUEUED jobs require an explicit Start and PAUSED
// jobs require Resume. Recovery is idempotent; already active jobs are not
// given a second goroutine.
func (m *Manager) Recover(ctx context.Context) error {
	if err := m.requireOpen(); err != nil {
		return err
	}
	summaries, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	var recoverErr error
	for _, summary := range summaries {
		if summary.Quarantine != nil {
			continue
		}
		if summary.Status != jobs.JobStatusRunning && summary.Status != jobs.JobStatusWaiting &&
			summary.Status != jobs.JobStatusQueued && summary.Status != jobs.JobStatusPaused {
			continue
		}
		var recoverJobErr error
		if summary.Status == jobs.JobStatusQueued || summary.Status == jobs.JobStatusPaused {
			if expirer, ok := m.runner.(dueExpirer); ok {
				_, _, recoverJobErr = expirer.ExpireDue(ctx, summary.ID)
			}
		} else if summary.Status == jobs.JobStatusRunning {
			_, recoverJobErr = m.Start(ctx, summary.ID)
		} else {
			var state jobs.JobState
			state, recoverJobErr = m.store.Load(ctx, summary.ID)
			if recoverJobErr == nil && state.Status == jobs.JobStatusWaiting {
				var entry *runEntry
				var admitted bool
				entry, admitted, recoverJobErr = m.admit(state, false)
				if recoverJobErr == nil && admitted {
					go m.run(entry)
				}
			} else if recoverJobErr == nil {
				continue
			}
		}
		if recoverJobErr != nil {
			// The state can legitimately become dormant/terminal between List
			// and Load. Such a race needs no recovery worker.
			if errors.Is(recoverJobErr, ErrNotStartable) {
				continue
			}
			// A job can be damaged or removed out-of-band between its independent
			// List validation and Start load. Keep recovery fail-closed for that
			// job without tearing down workers already admitted for healthy jobs.
			if errors.Is(recoverJobErr, jobstore.ErrCorrupt) || errors.Is(recoverJobErr, jobstore.ErrNotFound) {
				continue
			}
			recoverErr = errors.Join(recoverErr, fmt.Errorf("recover job %s: %w", summary.ID, recoverJobErr))
		}
	}
	return recoverErr
}

func (m *Manager) Create(ctx context.Context, spec jobs.JobSpec) (View, error) {
	if err := m.requireOpen(); err != nil {
		return View{}, err
	}
	state, err := m.store.Create(ctx, spec)
	if err != nil {
		return View{}, err
	}
	return m.annotate(state), nil
}

func (m *Manager) Get(ctx context.Context, jobID string) (View, error) {
	state, err := m.store.Load(ctx, jobID)
	if err != nil {
		return View{}, err
	}
	return m.annotate(state), nil
}

func (m *Manager) List(ctx context.Context) ([]Summary, error) {
	stored, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Summary, len(stored))
	for i, summary := range stored {
		_, active := m.active[summary.ID]
		lastError := m.lastErrors[summary.ID]
		if summary.Quarantine != nil {
			active = false
			lastError = summary.Quarantine.String()
		}
		out[i] = Summary{Job: summary, Active: active, LastError: lastError}
	}
	return out, nil
}

// Start admits QUEUED or already RUNNING durable state. A QUEUED start is not
// acknowledged until Runner has committed JobStarted; this removes the crash
// gap in which a caller could observe success while recovery still saw QUEUED.
// Repeated calls share the same startup barrier and never launch an additional
// goroutine.
func (m *Manager) Start(ctx context.Context, jobID string) (View, error) {
	if entry, err := m.activeEntry(jobID); entry != nil || err != nil {
		if err != nil {
			return View{}, err
		}
		view := m.entryView(entry)
		if view.State.Status != jobs.JobStatusQueued && view.State.Status != jobs.JobStatusRunning {
			return view, fmt.Errorf("%w: status %q", ErrNotStartable, view.State.Status)
		}
		return m.awaitStartup(ctx, entry)
	}
	state, err := m.store.Load(ctx, jobID)
	if err != nil {
		return View{}, err
	}
	if state.Status != jobs.JobStatusQueued && state.Status != jobs.JobStatusRunning {
		return m.annotate(state), fmt.Errorf("%w: status %q", ErrNotStartable, state.Status)
	}
	entry, admitted, err := m.admit(state, false)
	if err != nil {
		return View{}, err
	}
	if admitted {
		go m.run(entry)
	}
	return m.awaitStartup(ctx, entry)
}

// Cancel persists operator intent through Runner. It intentionally does not
// cancel Manager's server context; Runner owns interruption and durable drain
// semantics for any calls already in flight.
func (m *Manager) Cancel(ctx context.Context, jobID string) (View, error) {
	if err := m.requireOpen(); err != nil {
		return View{}, err
	}
	state, err := m.runner.RequestCancel(ctx, jobID)
	m.wakeJob(jobID)
	if err != nil {
		return m.annotate(state), err
	}
	return m.annotate(state), nil
}

// Pause is synchronous with the durable JobPaused event. If a batch is active,
// the request becomes a server-owned memory intent, the current batch drains,
// and RequestPause is called at the first boundary. Caller cancellation stops
// waiting but does not retract that accepted intent.
func (m *Manager) Pause(ctx context.Context, jobID string) (View, error) {
	entry, ok, err := m.requestPause(jobID)
	if err != nil {
		return View{}, err
	}
	if !ok {
		state, loadErr := m.store.Load(ctx, jobID)
		if loadErr != nil {
			return View{}, loadErr
		}
		if state.Status == jobs.JobStatusPaused {
			return m.annotate(state), nil
		}
		if state.Status.IsTerminal() {
			return m.annotate(state), fmt.Errorf("%w: status %q", ErrPauseFailed, state.Status)
		}
		var admitted bool
		entry, admitted, err = m.admit(state, true)
		if err != nil {
			return View{}, err
		}
		if admitted {
			go m.run(entry)
		}
	}

	select {
	case <-entry.done:
		return m.pauseResult(entry)
	default:
	}
	select {
	case <-entry.done:
		return m.pauseResult(entry)
	case <-ctx.Done():
		return m.entryView(entry), ctx.Err()
	}
}

// Resume durably resumes PAUSED/WAITING state and starts its background loop.
// If a graceful pause is still draining, Resume waits for that operation's
// linearization before applying the inverse transition.
func (m *Manager) Resume(ctx context.Context, jobID string) (View, error) {
	for {
		if err := m.requireOpen(); err != nil {
			return View{}, err
		}
		state, err := m.store.Load(ctx, jobID)
		if err != nil {
			return View{}, err
		}

		m.mu.Lock()
		entry := m.active[jobID]
		if entry != nil && entry.pauseRequested {
			done := entry.done
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return m.entryView(entry), ctx.Err()
			}
		}
		if entry != nil && state.Status == jobs.JobStatusWaiting {
			m.mu.Unlock()
			resumed, resumeErr := m.runner.RequestResume(ctx, jobID)
			if resumeErr != nil {
				// An automatic wake or another Resume may have won the revision
				// race. Reconcile that success, but never disguise an early resume
				// rejected by a persisted scheduled wake.
				if latest, loadErr := m.store.Load(ctx, jobID); loadErr == nil && latest.Status == jobs.JobStatusRunning {
					resumed = latest
					resumeErr = nil
				} else {
					if resumed.Spec.ID == "" {
						resumed = state
					}
					return m.annotate(resumed), fmt.Errorf("%w: %w", ErrNotControllable, resumeErr)
				}
			}

			m.mu.Lock()
			current := m.active[jobID]
			if current != nil {
				if resumed.Spec.ID != "" && resumed.Revision >= current.state.Revision {
					current.state = resumed
				}
				signalRunEntry(current)
				view := m.entryViewLocked(current)
				m.mu.Unlock()
				return view, nil
			}
			m.mu.Unlock()
			state = resumed
			if state.Status == jobs.JobStatusRunning || state.Status == jobs.JobStatusWaiting {
				entry, admitted, err := m.admit(state, false)
				if err != nil {
					return View{}, err
				}
				if admitted {
					go m.run(entry)
				}
				return m.entryView(entry), nil
			}
			return m.annotate(state), fmt.Errorf("%w: status %q", ErrNotStartable, state.Status)
		}
		if entry != nil && (state.Status == jobs.JobStatusPaused || state.Status.IsTerminal()) {
			done := entry.done
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return m.entryView(entry), ctx.Err()
			}
		}
		if entry != nil {
			view := m.entryViewLocked(entry)
			m.mu.Unlock()
			return view, nil
		}
		m.mu.Unlock()

		switch state.Status {
		case jobs.JobStatusPaused, jobs.JobStatusWaiting:
			resumed, err := m.runner.RequestResume(ctx, jobID)
			if err != nil {
				// StepRunner implementations own their coordination details. Export
				// one service-level classification so HTTP/CLI adapters can report
				// a state conflict without depending on jobruntime sentinels.
				if resumed.Spec.ID == "" {
					resumed = state
				}
				return m.annotate(resumed), fmt.Errorf("%w: %w", ErrNotControllable, err)
			}
			entry, admitted, err := m.admit(resumed, false)
			if err != nil {
				return View{}, err
			}
			if admitted {
				go m.run(entry)
			}
			return m.entryView(entry), nil
		case jobs.JobStatusQueued, jobs.JobStatusRunning:
			return m.Start(ctx, jobID)
		default:
			return m.annotate(state), fmt.Errorf("%w: status %q", ErrNotStartable, state.Status)
		}
	}
}

// Active reports whether this Manager currently owns a Step loop for jobID.
func (m *Manager) Active(jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.active[jobID]
	return ok
}

// LastError returns the most recent non-shutdown loop error. A successful new
// admission clears the previous value.
func (m *Manager) LastError(jobID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErrors[jobID]
}

// Shutdown stops all server-owned loops without recording operator
// cancellation. It is idempotent. The caller retains ownership of Store.Close.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		m.mu.Unlock()
		go func() {
			m.wg.Wait()
			close(m.stopped)
		}()
	})
	// Once cleanup has completed, report that fact even if a later caller
	// happens to supply an already-cancelled wait context.
	select {
	case <-m.stopped:
		return nil
	default:
	}
	select {
	case <-m.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) run(entry *runEntry) {
	defer m.wg.Done()
	var consecutiveErrors uint64
	for {
		m.mu.Lock()
		state := entry.state
		switch {
		case state.Status.IsTerminal():
			m.completeStartupLocked(entry, fmt.Errorf("%w: status %q", ErrNotStartable, state.Status))
			m.finishLocked(entry, "", false)
			m.mu.Unlock()
			return
		case entry.pauseRequested && state.Status == jobs.JobStatusPaused:
			m.completeStartupLocked(entry, fmt.Errorf("%w: status %q", ErrNotStartable, state.Status))
			m.finishLocked(entry, "", true)
			m.mu.Unlock()
			return
		case !entry.pauseRequested && state.Status == jobs.JobStatusPaused:
			m.completeStartupLocked(entry, fmt.Errorf("%w: status %q", ErrNotStartable, state.Status))
			m.finishLocked(entry, "", false)
			m.mu.Unlock()
			return
		}
		// A QUEUED job must first commit JobStarted. Calling RequestPause on
		// QUEUED would either fail or create an invalid transition, and would
		// also leave Start callers without a durable start linearization point.
		pauseAtBoundary := entry.pauseRequested && state.Status != jobs.JobStatusQueued && state.CurrentBatch == nil
		m.mu.Unlock()

		var (
			next       jobs.JobState
			progressed bool
			err        error
		)
		if pauseAtBoundary {
			next, err = m.runner.RequestPause(m.ctx, entry.jobID)
		} else if state.Status == jobs.JobStatusWaiting {
			next, progressed, err = m.advanceWaiting(entry, state)
		} else {
			next, progressed, err = m.runner.Step(m.ctx, entry.jobID)
		}
		if err != nil && pauseAtBoundary && !m.shutdownError(err) {
			// Cancellation or another durable control transition can win the
			// revision race with RequestPause. Reconcile before classifying the
			// loop as failed: a terminal state supersedes pause, PAUSED means the
			// intent committed, and a changed non-terminal revision is retried.
			if latest, loadErr := m.store.Load(m.ctx, entry.jobID); loadErr == nil &&
				(latest.Status.IsTerminal() || latest.Status == jobs.JobStatusPaused || latest.Revision != state.Revision) {
				next = latest
				err = nil
			}
		}
		if err != nil && !m.shutdownError(err) {
			// Step may have committed one or more durable events before reporting
			// a transport, coordination, or post-commit durability error. Always
			// reload canonical state before deciding whether a non-terminal error
			// is safe to retry.
			latest, loadErr := m.store.Load(m.ctx, entry.jobID)
			if loadErr == nil {
				next = latest
			} else {
				err = errors.Join(err, fmt.Errorf("reload durable state: %w", loadErr))
			}
		}

		m.mu.Lock()
		// Some Runner control failures occur before Load and therefore return
		// a zero state. Preserve the last canonical observation in that case.
		if next.Spec.ID != "" {
			entry.state = next
		}
		if entry.state.Status == jobs.JobStatusRunning {
			m.completeStartupLocked(entry, nil)
		}
		if err != nil {
			if m.shutdownError(err) {
				m.completeStartupLocked(entry, ErrClosed)
				delete(m.lastErrors, entry.jobID)
				m.finishLocked(entry, "", false)
				m.mu.Unlock()
				return
			}
			text := safeError(err)
			if entry.state.Status.IsTerminal() || entry.state.Status == jobs.JobStatusPaused {
				m.completeStartupLocked(entry, err)
				m.finishLocked(entry, text, entry.pauseRequested && entry.state.Status == jobs.JobStatusPaused)
				m.mu.Unlock()
				return
			}
			if m.retryableStepError(err) {
				consecutiveErrors++
				m.lastErrors[entry.jobID] = text
				delay := effectiveRetryDelay(err, m.retryBackoff(consecutiveErrors), entry.state.Spec.Deadline, time.Now())
				m.mu.Unlock()
				if m.waitRetry(entry, delay) {
					continue
				}
				m.mu.Lock()
				m.completeStartupLocked(entry, ErrClosed)
				delete(m.lastErrors, entry.jobID)
				m.finishLocked(entry, "", false)
				m.mu.Unlock()
				return
			}
			if entry.state.Status == jobs.JobStatusWaiting {
				m.completeStartupLocked(entry, err)
				m.finishLocked(entry, text, false)
				m.mu.Unlock()
				return
			}
			m.completeStartupLocked(entry, err)
			m.finishLocked(entry, text, false)
			m.mu.Unlock()
			return
		}
		consecutiveErrors = 0
		delete(m.lastErrors, entry.jobID)
		if !pauseAtBoundary && !progressed {
			// Decide dormancy under the same lock used by requestPause so a
			// pause cannot be acknowledged into an entry which is exiting.
			if entry.pauseRequested && !next.Status.IsTerminal() && next.Status != jobs.JobStatusPaused {
				m.mu.Unlock()
				continue
			}
			m.finishLocked(entry, "", entry.pauseRequested && next.Status == jobs.JobStatusPaused)
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
}

func (m *Manager) requestPause(jobID string) (*runEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrClosed
	}
	entry := m.active[jobID]
	if entry == nil {
		return nil, false, nil
	}
	entry.pauseRequested = true
	signalRunEntry(entry)
	return entry, true, nil
}

func (m *Manager) admit(state jobs.JobState, pause bool) (*runEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false, ErrClosed
	}
	if entry := m.active[state.Spec.ID]; entry != nil {
		if pause {
			entry.pauseRequested = true
			signalRunEntry(entry)
		}
		return entry, false, nil
	}
	entry := &runEntry{
		jobID:          state.Spec.ID,
		done:           make(chan struct{}),
		wake:           make(chan struct{}, 1),
		state:          state,
		startupDone:    make(chan struct{}),
		pauseRequested: pause,
	}
	if state.Status == jobs.JobStatusRunning || state.Status == jobs.JobStatusWaiting {
		m.completeStartupLocked(entry, nil)
	}
	m.active[state.Spec.ID] = entry
	delete(m.lastErrors, state.Spec.ID)
	m.wg.Add(1)
	return entry, true, nil
}

func (m *Manager) finishLocked(entry *runEntry, errText string, pauseSucceeded bool) {
	if !startupComplete(entry) {
		if m.closed {
			m.completeStartupLocked(entry, ErrClosed)
		} else {
			m.completeStartupLocked(entry, fmt.Errorf("%w: status %q", ErrNotStartable, entry.state.Status))
		}
	}
	entry.pauseSucceeded = pauseSucceeded
	entry.exitError = errText
	if errText != "" {
		m.lastErrors[entry.jobID] = errText
	}
	if m.active[entry.jobID] == entry {
		delete(m.active, entry.jobID)
	}
	close(entry.done)
}

func (m *Manager) pauseResult(entry *runEntry) (View, error) {
	m.mu.Lock()
	view := m.entryViewLocked(entry)
	succeeded := entry.pauseSucceeded
	exitError := entry.exitError
	closed := m.closed
	m.mu.Unlock()
	if succeeded {
		return view, nil
	}
	if closed && exitError == "" {
		return view, ErrClosed
	}
	if exitError != "" {
		return view, fmt.Errorf("%w: %s", ErrPauseFailed, exitError)
	}
	return view, fmt.Errorf("%w: status %q", ErrPauseFailed, view.State.Status)
}

func (m *Manager) activeEntry(jobID string) (*runEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	return m.active[jobID], nil
}

func (m *Manager) awaitStartup(ctx context.Context, entry *runEntry) (View, error) {
	select {
	case <-entry.startupDone:
		m.mu.Lock()
		view := m.entryViewLocked(entry)
		err := entry.startupErr
		m.mu.Unlock()
		return view, err
	case <-ctx.Done():
		return m.entryView(entry), ctx.Err()
	}
}

func (m *Manager) completeStartupLocked(entry *runEntry, err error) {
	if startupComplete(entry) {
		return
	}
	entry.startupErr = err
	close(entry.startupDone)
}

func startupComplete(entry *runEntry) bool {
	select {
	case <-entry.startupDone:
		return true
	default:
		return false
	}
}

func (m *Manager) annotate(state jobs.JobState) View {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, active := m.active[state.Spec.ID]
	return View{State: state, Active: active, LastError: m.lastErrors[state.Spec.ID]}
}

func (m *Manager) entryView(entry *runEntry) View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entryViewLocked(entry)
}

func (m *Manager) entryViewLocked(entry *runEntry) View {
	_, active := m.active[entry.jobID]
	lastError := m.lastErrors[entry.jobID]
	if entry.exitError != "" {
		lastError = entry.exitError
	}
	return View{State: entry.state, Active: active, LastError: lastError}
}

func (m *Manager) requireOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	return nil
}

func (m *Manager) shutdownError(err error) bool {
	return m.ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

func (m *Manager) retryableStepError(err error) bool {
	return err != nil && m.retryClassifier != nil && m.retryClassifier.RetryableStepError(err)
}

func (m *Manager) retryBackoff(consecutiveErrors uint64) time.Duration {
	delay := m.retryInitialBackoff
	for attempt := uint64(1); attempt < consecutiveErrors && delay < m.retryMaxBackoff; attempt++ {
		if delay > m.retryMaxBackoff/2 {
			return m.retryMaxBackoff
		}
		delay *= 2
	}
	return min(delay, m.retryMaxBackoff)
}

func effectiveRetryDelay(err error, base time.Duration, deadline, now time.Time) time.Duration {
	delay := base
	var suggested retryDelayError
	if errors.As(err, &suggested) && suggested.RetryDelay() > delay {
		delay = suggested.RetryDelay()
	}
	if remaining := deadline.Sub(now); remaining <= 0 {
		return 0
	} else if delay > remaining {
		return remaining
	}
	return delay
}

// advanceWaiting owns no model/provider work. It waits on persisted scheduler
// time, an explicit control wake, or the hard deadline, then reconciles durable
// state before taking exactly one transition. A zero NextWakeAt is a manual
// wait: only Resume can make it RUNNING, but the deadline is still enforced.
func (m *Manager) advanceWaiting(entry *runEntry, observed jobs.JobState) (jobs.JobState, bool, error) {
	target := observed.Spec.Deadline
	if !observed.NextWakeAt.IsZero() && observed.NextWakeAt.Before(target) {
		target = observed.NextWakeAt
	}
	delay := time.Until(target)
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-entry.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-m.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return observed, false, m.ctx.Err()
		}
	}

	latest, err := m.store.Load(m.ctx, entry.jobID)
	if err != nil {
		return observed, false, err
	}
	if latest.Status != jobs.JobStatusWaiting {
		return latest, true, nil
	}
	now := time.Now().UTC()
	if !now.Before(latest.Spec.Deadline) {
		return m.runner.Step(m.ctx, entry.jobID)
	}
	if latest.NextWakeAt.IsZero() || now.Before(latest.NextWakeAt) {
		// A control signal may be intentionally delivered before its durable
		// mutation is visible. Return to the run loop so pause intent is honored
		// and any one stale buffered signal is consumed without spinning.
		return latest, true, nil
	}

	resumed, err := m.runner.RequestResume(m.ctx, entry.jobID)
	if err == nil {
		return resumed, true, nil
	}
	// External Resume or Cancel can win the revision race with the timer. Its
	// canonical state is progress; a still-waiting state preserves the error so
	// the normal bounded retry policy can decide what to do.
	if reconciled, loadErr := m.store.Load(m.ctx, entry.jobID); loadErr == nil {
		if reconciled.Status != jobs.JobStatusWaiting || reconciled.Revision != latest.Revision {
			return reconciled, true, nil
		}
		return reconciled, false, err
	} else {
		return latest, false, errors.Join(err, fmt.Errorf("reload durable state: %w", loadErr))
	}
}

func (m *Manager) waitRetry(entry *runEntry, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-timer.C:
		return true
	case <-entry.wake:
		return true
	case <-m.ctx.Done():
		return false
	}
}

func (m *Manager) wakeJob(jobID string) {
	m.mu.Lock()
	entry := m.active[jobID]
	if entry != nil {
		signalRunEntry(entry)
	}
	m.mu.Unlock()
}

func signalRunEntry(entry *runEntry) {
	select {
	case entry.wake <- struct{}{}:
	default:
	}
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(secrets.Redact(err.Error()))
}
