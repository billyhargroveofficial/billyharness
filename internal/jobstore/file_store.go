package jobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

const (
	specFileName       = "spec.json"
	eventsFileName     = "events.jsonl"
	snapshotFileName   = "snapshot.json"
	artifactsDirectory = "artifacts"
	maxSnapshotBytes   = 64 << 20
)

// FileStore is the single-owner filesystem implementation of Store. All
// operations are serialized inside the process and the root ownership lock
// excludes a second process. The event log remains authoritative even when a
// snapshot refresh is interrupted.
type FileStore struct {
	mu        sync.Mutex
	root      string
	options   Options
	owner     *ownershipLock
	artifacts *artifactStore
	cache     map[string]*cachedJob
	// fullReplayCount is test-observable only. It makes the performance
	// invariant deterministic without timing-sensitive benchmarks.
	fullReplayCount uint64
	closed          bool
}

var _ Store = (*FileStore)(nil)

// CoordinationKey identifies this canonical filesystem namespace. FileStore's
// exclusive ownership lock guarantees that the canonical root is a unique
// durable backend identity for its lifetime.
func (s *FileStore) CoordinationKey() string {
	if s == nil || s.root == "" {
		return ""
	}
	return "file:" + s.root
}

// ProtectedRoots returns a fresh copy of the canonical store root. The root is
// immutable for the lifetime of FileStore and remains available after Close so
// authority construction cannot accidentally become less restrictive.
func (s *FileStore) ProtectedRoots() []string {
	if s == nil || s.root == "" {
		return nil
	}
	return []string{s.root}
}

// NewFileStore opens root for exclusive use until Close. The root is created
// with private permissions when it does not exist.
func NewFileStore(root string, options Options) (*FileStore, error) {
	if err := validateStorePlatform(); err != nil {
		return nil, err
	}
	resolved, err := options.Resolve()
	if err != nil {
		return nil, err
	}
	owner, err := acquireOwnership(root)
	if err != nil {
		return nil, err
	}
	if err := cleanupAbandonedStaging(owner.root); err != nil {
		_ = owner.Close()
		return nil, fmt.Errorf("clean abandoned job staging: %w", err)
	}
	store := &FileStore{
		root:    owner.root,
		options: resolved,
		cache:   make(map[string]*cachedJob),
		owner:   owner,
	}
	store.artifacts = newArtifactStore(store.root, resolved.MaxArtifactBytes)
	return store, nil
}

// Close releases process ownership. It is safe to call concurrently and more
// than once; an operation already holding the store mutex finishes first.
func (s *FileStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	clear(s.cache)
	if s.owner == nil {
		return nil
	}
	err := s.owner.Close()
	s.owner = nil
	return err
}

func (s *FileStore) Create(ctx context.Context, spec jobs.JobSpec) (jobs.JobState, error) {
	if err := contextError(ctx); err != nil {
		return jobs.JobState{}, err
	}
	if err := spec.Validate(); err != nil {
		return jobs.JobState{}, fmt.Errorf("validate job spec: %w", err)
	}
	if err := ValidatePortableID(spec.ID); err != nil {
		return jobs.JobState{}, fmt.Errorf("job id: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return jobs.JobState{}, err
	}
	if err := contextError(ctx); err != nil {
		return jobs.JobState{}, err
	}

	finalDir, err := s.jobPath(spec.ID)
	if err != nil {
		return jobs.JobState{}, err
	}
	if _, err := os.Lstat(finalDir); err == nil {
		return jobs.JobState{}, fmt.Errorf("%w: job %q", ErrAlreadyExists, spec.ID)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return jobs.JobState{}, fmt.Errorf("inspect job destination: %w", err)
	}

	envelope, err := NewSpecEnvelope(spec)
	if err != nil {
		return jobs.JobState{}, err
	}
	state := jobs.JobState{Spec: spec, Status: jobs.JobStatusQueued}
	if err := state.Validate(); err != nil {
		return jobs.JobState{}, fmt.Errorf("validate initial job state: %w", err)
	}
	snapshot := SnapshotEnvelope{
		SchemaVersion: SchemaVersion,
		JobID:         spec.ID,
		Seq:           0,
		LastHash:      envelope.SpecHash,
		State:         state,
	}

	tempDir, err := os.MkdirTemp(s.root, ".creating-"+spec.ID+"-")
	if err != nil {
		return jobs.JobState{}, fmt.Errorf("create job staging directory: %w", err)
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return jobs.JobState{}, fmt.Errorf("secure job staging directory: %w", err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, artifactsDirectory), 0o700); err != nil {
		return jobs.JobState{}, fmt.Errorf("create artifacts directory: %w", err)
	}
	if err := writeJSONExclusive(filepath.Join(tempDir, specFileName), envelope, int64(s.options.MaxRecordBytes)); err != nil {
		return jobs.JobState{}, fmt.Errorf("write job spec: %w", err)
	}
	if err := createEmptyDurableFile(filepath.Join(tempDir, eventsFileName)); err != nil {
		return jobs.JobState{}, fmt.Errorf("create job event log: %w", err)
	}
	if err := writeJSONExclusive(filepath.Join(tempDir, snapshotFileName), snapshot, maxSnapshotBytes); err != nil {
		return jobs.JobState{}, fmt.Errorf("write initial job snapshot: %w", err)
	}
	if err := syncDirectory(filepath.Join(tempDir, artifactsDirectory)); err != nil {
		return jobs.JobState{}, fmt.Errorf("sync artifacts directory: %w", err)
	}
	if err := syncDirectory(tempDir); err != nil {
		return jobs.JobState{}, fmt.Errorf("sync job staging directory: %w", err)
	}
	if err := publishDirectoryNoReplace(tempDir, finalDir); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, ErrAlreadyExists) {
			return jobs.JobState{}, fmt.Errorf("%w: job %q", ErrAlreadyExists, spec.ID)
		}
		return jobs.JobState{}, fmt.Errorf("publish job directory: %w", err)
	}
	removeTemp = false
	if err := syncDirectory(s.root); err != nil {
		return cloneStateForStore(state), &CommitError{
			Operation: "create",
			JobID:     spec.ID,
			Revision:  state.Revision,
			Err:       fmt.Errorf("sync job store root: %w", err),
		}
	}
	return state, nil
}

func (s *FileStore) Append(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	event jobs.Event,
) (jobs.JobState, error) {
	if err := contextError(ctx); err != nil {
		return jobs.JobState{}, err
	}
	if err := ValidatePortableID(jobID); err != nil {
		return jobs.JobState{}, fmt.Errorf("job id: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return jobs.JobState{}, err
	}
	loaded, err := s.loadLocked(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	jobDir, err := s.jobPath(jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	eventsPath := filepath.Join(jobDir, eventsFileName)

	// A caller may not know whether the commit response was delivered, and a
	// later event may have committed before that caller reconciles. An exact
	// retry of any persisted event is therefore successful at its original
	// expected revision. Reusing an ID with different content or revision always
	// conflicts.
	if persisted, ok := loaded.eventsByID[event.ID]; ok {
		matches, matchErr := persisted.matches(eventsPath, expectedRevision, event, s.options.MaxRecordBytes)
		if matchErr != nil {
			delete(s.cache, jobID)
			return jobs.JobState{}, corruptionAt(jobID, eventsPath, CorruptionMalformedJSON, matchErr)
		}
		if matches {
			return cloneStateForStore(loaded.state), nil
		}
		return jobs.JobState{}, &ConflictError{
			JobID:            jobID,
			ExpectedRevision: expectedRevision,
			ActualRevision:   loaded.state.Revision,
		}
	}
	if expectedRevision != loaded.state.Revision {
		return jobs.JobState{}, &ConflictError{
			JobID:            jobID,
			ExpectedRevision: expectedRevision,
			ActualRevision:   loaded.state.Revision,
		}
	}
	next, err := jobs.Reduce(loaded.state, event)
	if err != nil {
		return jobs.JobState{}, err
	}
	if next.Revision != expectedRevision+1 {
		return jobs.JobState{}, fmt.Errorf("%w: reducer revision %d did not follow %d", ErrConflict, next.Revision, expectedRevision)
	}
	record, err := NewEventRecord(
		jobID,
		loaded.seq+1,
		expectedRevision,
		next.Revision,
		loaded.lastHash,
		event,
	)
	if err != nil {
		return jobs.JobState{}, fmt.Errorf("build event record: %w", err)
	}
	previousEventLogBytes := loaded.eventLogBytes
	eventLogBytes, err := appendCanonicalEvent(eventsPath, record, s.options.MaxRecordBytes)
	if err != nil {
		// The write may have reached disk before an error was observed. Force a
		// canonical replay on reconciliation instead of trusting stale memory.
		delete(s.cache, jobID)
		return jobs.JobState{}, err
	}

	snapshot := SnapshotEnvelope{
		SchemaVersion: SchemaVersion,
		JobID:         jobID,
		Seq:           record.Seq,
		LastHash:      record.Hash,
		State:         next,
	}
	// Snapshot is a disposable cache. Once the canonical JSONL record is
	// fsynced the append succeeded; a failed refresh must not turn success into
	// an ambiguous retry or make a large but valid job unloadable.
	snapshotCurrent := false
	if eventLogBytes <= maxSnapshotBytes/2 {
		snapshotCurrent = writeJSONAtomic(filepath.Join(jobDir, snapshotFileName), snapshot, maxSnapshotBytes) == nil
	}
	loaded.state = next
	loaded.seq = record.Seq
	loaded.lastHash = record.Hash
	loaded.eventLogBytes = eventLogBytes
	if eventLogBytes > previousEventLogBytes &&
		eventLogBytes-previousEventLogBytes <= int64(s.options.MaxRecordBytes)+1 {
		loaded.eventsByID[event.ID] = persistedEvent{
			ExpectedRevision: expectedRevision,
			Offset:           previousEventLogBytes,
			ByteCount:        eventLogBytes - previousEventLogBytes,
		}
	} else {
		// The canonical append is committed, but its post-write size could not
		// be established. Do not retain an incomplete idempotence index.
		delete(s.cache, jobID)
		return cloneStateForStore(next), nil
	}
	s.updateCacheAfterAppendLocked(jobID, jobDir, loaded, snapshotCurrent)
	return cloneStateForStore(next), nil
}

func (s *FileStore) Load(ctx context.Context, jobID string) (jobs.JobState, error) {
	if err := contextError(ctx); err != nil {
		return jobs.JobState{}, err
	}
	if err := ValidatePortableID(jobID); err != nil {
		return jobs.JobState{}, fmt.Errorf("job id: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return jobs.JobState{}, err
	}
	loaded, err := s.loadLocked(ctx, jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	return cloneStateForStore(loaded.state), nil
}

func (s *FileStore) List(ctx context.Context) ([]JobSummary, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list job store: %w", err)
	}
	summaries := make([]JobSummary, 0, len(entries))
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if err := ValidatePortableID(name); err != nil {
			summaries = append(summaries, quarantinedJobSummary(name, NewCorruptionError(CorruptionMetadata{
				Path: filepath.Join(s.root, name),
				Kind: CorruptionIdentityMismatch,
			}, fmt.Errorf("invalid job directory name: %w", err))))
			continue
		}
		if !entry.IsDir() {
			summaries = append(summaries, quarantinedJobSummary(name, NewCorruptionError(CorruptionMetadata{
				JobID: name,
				Path:  filepath.Join(s.root, name),
				Kind:  CorruptionIdentityMismatch,
			}, errors.New("job entry is not a directory"))))
			continue
		}
		loaded, err := s.loadLocked(ctx, name)
		if err != nil {
			if contextError(ctx) != nil {
				return nil, contextError(ctx)
			}
			// ReadDir succeeded, so this failure is scoped to one job entry.
			// Quarantine it and continue; root-level failures above still abort.
			summaries = append(summaries, quarantinedJobSummary(name, err))
			continue
		}
		state := loaded.state
		summaries = append(summaries, JobSummary{
			ID:             state.Spec.ID,
			Goal:           state.Spec.Goal,
			Preset:         state.Spec.Preset,
			Status:         state.Status,
			TerminalReason: state.TerminalReason,
			Revision:       state.Revision,
			Cycle:          state.Cycle,
			Usage:          state.Usage,
			AdmittedAt:     state.Spec.AdmittedAt,
			Deadline:       state.Spec.Deadline,
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ID < summaries[j].ID })
	return summaries, nil
}

func (s *FileStore) PutArtifact(
	ctx context.Context,
	jobID, artifactID, mediaType, createdByAttemptID string,
	reader io.Reader,
) (jobs.ArtifactRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if _, err := s.loadLocked(ctx, jobID); err != nil {
		return jobs.ArtifactRef{}, err
	}
	return s.artifacts.Put(ctx, jobID, artifactID, mediaType, createdByAttemptID, reader)
}

func (s *FileStore) OpenArtifact(
	ctx context.Context,
	jobID, artifactID string,
) (io.ReadCloser, jobs.ArtifactRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	if _, err := s.loadLocked(ctx, jobID); err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	return s.artifacts.Open(ctx, jobID, artifactID)
}

type loadedJob struct {
	state         jobs.JobState
	seq           uint64
	lastHash      string
	eventsByID    map[string]persistedEvent
	eventLogBytes int64
}

type persistedEvent struct {
	ExpectedRevision uint64
	Offset           int64
	ByteCount        int64
}

func (p persistedEvent) matches(
	eventsPath string,
	expectedRevision uint64,
	event jobs.Event,
	maxRecordBytes int,
) (bool, error) {
	if expectedRevision != p.ExpectedRevision {
		return false, nil
	}
	if p.Offset < 0 || p.ByteCount <= 1 || p.ByteCount > int64(maxRecordBytes)+1 {
		return false, errors.New("cached event location is invalid")
	}
	file, err := openRegularRead(eventsPath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	if _, err := file.Seek(p.Offset, io.SeekStart); err != nil {
		return false, fmt.Errorf("seek cached event record: %w", err)
	}
	body := make([]byte, p.ByteCount)
	if _, err := io.ReadFull(file, body); err != nil {
		return false, fmt.Errorf("read cached event record: %w", err)
	}
	if body[len(body)-1] != '\n' {
		return false, errors.New("cached event record is not newline-terminated")
	}
	record, _, err := decodeCanonicalEventRecord(body[:len(body)-1])
	if err != nil {
		return false, err
	}
	if err := record.Validate(); err != nil {
		return false, fmt.Errorf("validate cached event record: %w", err)
	}
	return record.ExpectedRevision == expectedRevision && canonicalJSONEqual(record.Event, event), nil
}

type durableFileVersion struct {
	info    os.FileInfo
	size    int64
	modTime int64
}

type cachedJob struct {
	loaded          loadedJob
	specVersion     durableFileVersion
	eventsVersion   durableFileVersion
	snapshotVersion durableFileVersion
	snapshotCurrent bool
}

func (s *FileStore) loadLocked(ctx context.Context, jobID string) (loadedJob, error) {
	if err := contextError(ctx); err != nil {
		return loadedJob{}, err
	}
	jobDir, err := s.existingJobDir(jobID)
	if err != nil {
		return loadedJob{}, err
	}
	if cached, ok := s.cachedJobLocked(jobID, jobDir); ok {
		return cached, nil
	}
	return s.replayJobLocked(jobID, jobDir)
}

func (s *FileStore) replayJobLocked(jobID, jobDir string) (loadedJob, error) {
	s.fullReplayCount++
	specPath := filepath.Join(jobDir, specFileName)
	var spec SpecEnvelope
	if err := readStrictJSONFile(specPath, int64(s.options.MaxRecordBytes), &spec); err != nil {
		return loadedJob{}, corruptionAt(jobID, specPath, CorruptionMalformedJSON, err)
	}
	if err := spec.Validate(); err != nil {
		return loadedJob{}, corruptionAt(jobID, specPath, CorruptionHashMismatch, err)
	}
	if spec.JobID != jobID {
		return loadedJob{}, corruptionAt(jobID, specPath, CorruptionIdentityMismatch, fmt.Errorf(
			"spec job id %q does not match directory %q", spec.JobID, jobID,
		))
	}

	eventsPath := filepath.Join(jobDir, eventsFileName)
	events, err := openRegularReadWrite(eventsPath)
	if err != nil {
		return loadedJob{}, corruptionAt(jobID, eventsPath, CorruptionMalformedJSON, err)
	}
	eventsInfo, err := events.Stat()
	if err != nil {
		_ = events.Close()
		return loadedJob{}, fmt.Errorf("stat job events: %w", err)
	}
	replay, replayErr := Replay(spec, events, s.options.MaxRecordBytes)
	if replayErr != nil {
		_ = events.Close()
		return loadedJob{}, addCorruptionPath(replayErr, eventsPath)
	}
	if replay.Tail != nil {
		currentInfo, statErr := events.Stat()
		if statErr != nil || !os.SameFile(eventsInfo, currentInfo) || currentInfo.Size() != eventsInfo.Size() {
			_ = events.Close()
			if statErr != nil {
				return loadedJob{}, fmt.Errorf("restat event log before recovery: %w", statErr)
			}
			return loadedJob{}, fmt.Errorf("%w: event log changed during tail recovery", ErrConflict)
		}
		if err := events.Truncate(replay.Tail.TruncateOffset); err != nil {
			_ = events.Close()
			return loadedJob{}, fmt.Errorf("recover partial event tail: %w", err)
		}
		if err := events.Sync(); err != nil {
			_ = events.Close()
			return loadedJob{}, fmt.Errorf("sync recovered event log: %w", err)
		}
		replay.Tail = nil
	}
	if err := events.Close(); err != nil {
		return loadedJob{}, fmt.Errorf("close job events: %w", err)
	}
	if replay.Tail == nil {
		// The prior Stat preceded possible tail truncation.
		eventsInfo, err = statRegularFile(eventsPath)
		if err != nil {
			return loadedJob{}, corruptionAt(jobID, eventsPath, CorruptionMalformedJSON, err)
		}
	}

	eventLogBytes := eventsInfo.Size()
	snapshotCurrent := false
	if replay.Tail == nil && eventLogBytes <= maxSnapshotBytes/2 {
		wantSnapshot := SnapshotEnvelope{
			SchemaVersion: SchemaVersion,
			JobID:         jobID,
			Seq:           replay.Seq,
			LastHash:      replay.LastHash,
			State:         replay.State,
		}
		snapshotPath := filepath.Join(jobDir, snapshotFileName)
		var snapshot SnapshotEnvelope
		snapshotErr := readStrictJSONFile(snapshotPath, maxSnapshotBytes, &snapshot)
		validSnapshot := snapshotErr == nil && snapshot.Validate() == nil && canonicalJSONEqual(snapshot, wantSnapshot)
		if !validSnapshot {
			// Event replay is canonical. Snapshot repair is deliberately best effort
			// so cache damage or a read-only cache file cannot make a valid job unreadable.
			snapshotCurrent = writeJSONAtomic(snapshotPath, wantSnapshot, maxSnapshotBytes) == nil
		} else {
			snapshotCurrent = true
		}
	}
	eventsByID := make(map[string]persistedEvent, len(replay.Records))
	var eventOffset int64
	for _, record := range replay.Records {
		canonical, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			return loadedJob{}, fmt.Errorf("index canonical event record: %w", marshalErr)
		}
		byteCount := int64(len(canonical) + 1)
		eventsByID[record.Event.ID] = persistedEvent{
			ExpectedRevision: record.ExpectedRevision,
			Offset:           eventOffset,
			ByteCount:        byteCount,
		}
		eventOffset += byteCount
	}
	if eventOffset != eventLogBytes {
		return loadedJob{}, corruptionAt(jobID, eventsPath, CorruptionSequenceGap, fmt.Errorf(
			"indexed event bytes %d do not match canonical log size %d", eventOffset, eventLogBytes,
		))
	}
	loaded := loadedJob{
		state:         replay.State,
		seq:           replay.Seq,
		lastHash:      replay.LastHash,
		eventsByID:    eventsByID,
		eventLogBytes: eventLogBytes,
	}
	s.cacheLoadedJobLocked(jobID, jobDir, loaded, snapshotCurrent)
	return loaded, nil
}

// cachedJobLocked returns only state anchored to the same immutable spec file
// and append-only event-log version that was fully replayed. The exclusive
// root ownership lock is the writer-fencing primitive; file versions make
// accidental/out-of-band replacement fail back to canonical replay.
func (s *FileStore) cachedJobLocked(jobID, jobDir string) (loadedJob, bool) {
	cached := s.cache[jobID]
	if cached == nil {
		return loadedJob{}, false
	}
	specVersion, err := regularFileVersion(filepath.Join(jobDir, specFileName))
	if err != nil || !sameDurableFileVersion(cached.specVersion, specVersion) {
		delete(s.cache, jobID)
		return loadedJob{}, false
	}
	eventsVersion, err := regularFileVersion(filepath.Join(jobDir, eventsFileName))
	if err != nil || !sameDurableFileVersion(cached.eventsVersion, eventsVersion) {
		delete(s.cache, jobID)
		return loadedJob{}, false
	}

	if cached.loaded.eventLogBytes <= maxSnapshotBytes/2 {
		snapshotPath := filepath.Join(jobDir, snapshotFileName)
		current, snapshotErr := regularFileVersion(snapshotPath)
		if !cached.snapshotCurrent || snapshotErr != nil ||
			!sameDurableFileVersion(cached.snapshotVersion, current) {
			want := SnapshotEnvelope{
				SchemaVersion: SchemaVersion,
				JobID:         jobID,
				Seq:           cached.loaded.seq,
				LastHash:      cached.loaded.lastHash,
				State:         cached.loaded.state,
			}
			if writeJSONAtomic(snapshotPath, want, maxSnapshotBytes) == nil {
				if current, snapshotErr = regularFileVersion(snapshotPath); snapshotErr == nil {
					cached.snapshotVersion = current
					cached.snapshotCurrent = true
				}
			} else {
				cached.snapshotCurrent = false
			}
		}
	}
	return cached.loaded, true
}

func (s *FileStore) cacheLoadedJobLocked(jobID, jobDir string, loaded loadedJob, snapshotCurrent bool) {
	if s.cache == nil {
		s.cache = make(map[string]*cachedJob)
	}
	specVersion, specErr := regularFileVersion(filepath.Join(jobDir, specFileName))
	eventsVersion, eventsErr := regularFileVersion(filepath.Join(jobDir, eventsFileName))
	if specErr != nil || eventsErr != nil || eventsVersion.size != loaded.eventLogBytes {
		delete(s.cache, jobID)
		return
	}
	cached := &cachedJob{
		loaded:          loaded,
		specVersion:     specVersion,
		eventsVersion:   eventsVersion,
		snapshotCurrent: snapshotCurrent,
	}
	if snapshotCurrent {
		if version, err := regularFileVersion(filepath.Join(jobDir, snapshotFileName)); err == nil {
			cached.snapshotVersion = version
		} else {
			cached.snapshotCurrent = false
		}
	}
	s.cache[jobID] = cached
}

func (s *FileStore) updateCacheAfterAppendLocked(
	jobID, jobDir string,
	loaded loadedJob,
	snapshotCurrent bool,
) {
	// Cache refresh is post-commit optimization only. Any failure invalidates
	// memory and leaves reconciliation to the canonical log on the next call.
	s.cacheLoadedJobLocked(jobID, jobDir, loaded, snapshotCurrent)
}

func regularFileVersion(path string) (durableFileVersion, error) {
	file, err := openRegularRead(path)
	if err != nil {
		return durableFileVersion{}, err
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return durableFileVersion{}, statErr
	}
	if closeErr != nil {
		return durableFileVersion{}, closeErr
	}
	return durableFileVersion{info: info, size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

func statRegularFile(path string) (os.FileInfo, error) {
	version, err := regularFileVersion(path)
	if err != nil {
		return nil, err
	}
	return version.info, nil
}

func sameDurableFileVersion(left, right durableFileVersion) bool {
	return left.info != nil && right.info != nil &&
		os.SameFile(left.info, right.info) &&
		left.size == right.size && left.modTime == right.modTime
}

func quarantinedJobSummary(jobID string, err error) JobSummary {
	report := QuarantineReport{Kind: CorruptionUnreadable}
	var corruption *CorruptionError
	if errors.As(err, &corruption) {
		report.Kind = corruption.Kind
		report.Line = corruption.Line
		report.Seq = corruption.Seq
		report.Offset = corruption.Offset
	}
	return JobSummary{ID: jobID, Quarantine: &report}
}

func (s *FileStore) ensureOpenLocked() error {
	if s == nil || s.closed || s.owner == nil {
		return ErrClosed
	}
	return nil
}

func (s *FileStore) jobPath(jobID string) (string, error) {
	if err := ValidatePortableID(jobID); err != nil {
		return "", fmt.Errorf("job id: %w", err)
	}
	return containedJoin(s.root, jobID)
}

func (s *FileStore) existingJobDir(jobID string) (string, error) {
	path, err := s.jobPath(jobID)
	if err != nil {
		return "", err
	}
	if err := requirePrivateDirectory(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: job %q", ErrNotFound, jobID)
		}
		return "", err
	}
	return path, nil
}

func appendCanonicalEvent(path string, record EventRecord, maxBytes int) (int64, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return 0, fmt.Errorf("encode event record: %w", err)
	}
	if len(body) > maxBytes {
		return 0, &TooLargeError{Resource: "event record", Limit: int64(maxBytes), Actual: int64(len(body))}
	}
	file, err := openRegularAppend(path)
	if err != nil {
		return 0, fmt.Errorf("open event log for append: %w", err)
	}
	writeErr := writeAll(file, append(body, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	info, statErr := file.Stat()
	_ = file.Close()
	if writeErr != nil {
		return 0, fmt.Errorf("append event record: %w", writeErr)
	}
	if statErr != nil {
		// The record is already fsynced; zero merely disables the size-based
		// snapshot optimization for this call.
		return maxSnapshotBytes, nil
	}
	return info.Size(), nil
}

func createEmptyDurableFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func writeJSONExclusive(path string, value any, maxBytes int64) error {
	body, err := marshalJSONFile(value, maxBytes)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := writeAll(file, body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func writeJSONAtomic(path string, value any, maxBytes int64) error {
	body, err := marshalJSONFile(value, maxBytes)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := requirePrivateDirectory(dir); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	writeErr := writeAll(temp, body)
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	closeErr := temp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	return syncDirectory(dir)
}

func marshalJSONFile(value any, maxBytes int64) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if int64(len(body)) > maxBytes {
		return nil, &TooLargeError{Resource: "JSON file", Limit: maxBytes, Actual: int64(len(body))}
	}
	return body, nil
}

func readStrictJSONFile(path string, maxBytes int64, destination any) error {
	file, err := openRegularRead(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= 0 {
		return errors.New("JSON file is empty")
	}
	if info.Size() > maxBytes {
		return &TooLargeError{Resource: path, Limit: maxBytes, Actual: info.Size()}
	}
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > maxBytes {
		return &TooLargeError{Resource: path, Limit: maxBytes, Actual: int64(len(body))}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func corruptionAt(jobID, path string, kind CorruptionKind, err error) error {
	return NewCorruptionError(CorruptionMetadata{JobID: jobID, Path: path, Kind: kind}, err)
}

func addCorruptionPath(err error, path string) error {
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		return err
	}
	copy := *corruption
	if copy.Path == "" {
		copy.Path = path
	}
	return &copy
}

func canonicalJSONEqual(left, right any) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBody, rightBody)
}

func cloneStateForStore(state jobs.JobState) jobs.JobState {
	// JSON round-tripping would be needlessly expensive on every call. All
	// mutable slices/pointers in the current domain are cloned explicitly.
	out := state
	out.Spec.Workflow.StageOrder = slices.Clone(state.Spec.Workflow.StageOrder)
	out.Spec.Workflow.WorkerRoleIDs = slices.Clone(state.Spec.Workflow.WorkerRoleIDs)
	out.Spec.Roles = slices.Clone(state.Spec.Roles)
	for index := range out.Spec.Roles {
		out.Spec.Roles[index].Authority = cloneAuthorityForStore(state.Spec.Roles[index].Authority)
	}
	out.Spec.Stages = slices.Clone(state.Spec.Stages)
	for index := range out.Spec.Stages {
		out.Spec.Stages[index].RoleIDs = slices.Clone(state.Spec.Stages[index].RoleIDs)
	}
	out.Spec.Authority = cloneAuthorityForStore(state.Spec.Authority)
	if state.CurrentBatch != nil {
		batch := cloneBatchForStore(*state.CurrentBatch)
		out.CurrentBatch = &batch
	}
	out.Attempts = slices.Clone(state.Attempts)
	for index := range out.Attempts {
		out.Attempts[index].Artifacts = slices.Clone(state.Attempts[index].Artifacts)
		out.Attempts[index].Decision = cloneDecisionForStore(state.Attempts[index].Decision)
	}
	out.CompletedBatches = slices.Clone(state.CompletedBatches)
	out.Artifacts = slices.Clone(state.Artifacts)
	out.LastDecision = cloneDecisionForStore(state.LastDecision)
	out.StagnationFingerprints = slices.Clone(state.StagnationFingerprints)
	return out
}

func cloneDecisionForStore(decision *jobs.Decision) *jobs.Decision {
	if decision == nil {
		return nil
	}
	out := *decision
	if decision.NextBatch != nil {
		batch := cloneBatchForStore(*decision.NextBatch)
		out.NextBatch = &batch
	}
	return &out
}

func cloneBatchForStore(batch jobs.WorkBatch) jobs.WorkBatch {
	out := batch
	out.Items = slices.Clone(batch.Items)
	for index := range out.Items {
		out.Items[index].Authority = cloneAuthorityForStore(batch.Items[index].Authority)
	}
	return out
}

func cloneAuthorityForStore(authority jobs.Authority) jobs.Authority {
	out := authority
	out.Tools = slices.Clone(authority.Tools)
	out.ReadRoots = slices.Clone(authority.ReadRoots)
	out.WriteRoots = slices.Clone(authority.WriteRoots)
	out.NetworkHosts = slices.Clone(authority.NetworkHosts)
	out.Providers = slices.Clone(authority.Providers)
	return out
}
