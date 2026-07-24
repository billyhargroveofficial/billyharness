package jobruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

// Action identifies the gateway operation which produced a JobResultMsg.
type Action string

const (
	ActionCreate Action = "create"
	ActionShow   Action = "show"
	ActionRun    Action = "run"
	ActionPause  Action = "pause"
	ActionResume Action = "resume"
	ActionCancel Action = "cancel"
)

// ListResultMsg is emitted by Client.ListCmd.
type ListResultMsg struct {
	Jobs []gatewayapi.JobSummaryResponse
	Err  error
}

// JobResultMsg is emitted by every command which operates on one job. JobID
// is always the requested ID, including the client-generated ID used by
// CreateCmd to make an ambiguous create safe to retry.
type JobResultMsg struct {
	Action   Action
	JobID    string
	Response gatewayapi.JobResponse
	Err      error
}

// Gateway is the typed durable-jobs surface used by the TUI adapter. A
// *gatewayclient.Client satisfies this interface.
type Gateway interface {
	CreateJob(context.Context, gatewayapi.CreateJobRequest) (gatewayapi.JobResponse, error)
	ListJobs(context.Context) ([]gatewayapi.JobSummaryResponse, error)
	GetJob(context.Context, string) (gatewayapi.JobResponse, error)
	RunJob(context.Context, string) (gatewayapi.JobResponse, error)
	PauseJob(context.Context, string) (gatewayapi.JobResponse, error)
	ResumeJob(context.Context, string) (gatewayapi.JobResponse, error)
	CancelJob(context.Context, string) (gatewayapi.JobResponse, error)
}

// Client converts synchronous gateway calls into Bubble Tea commands.
// Client is safe to copy; each command captures an immutable input snapshot.
type Client struct {
	gateway Gateway
}

// New connects the TUI adapter to a BillyHarness gateway URL. Authentication
// and ready-retry behavior remain owned by gatewayclient.
func New(gatewayURL string) Client {
	return WithGateway(gatewayclient.New(gatewayURL))
}

// WithGateway constructs an adapter around a typed gateway implementation.
// It is useful for injecting a custom HTTP transport or a deterministic test
// double without coupling UI code to concrete networking details.
func WithGateway(gateway Gateway) Client {
	return Client{gateway: gateway}
}

// ListCmd asynchronously lists all durable jobs, including paginated gateway
// results aggregated by gatewayclient.
func (c Client) ListCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		if err := c.ready(ctx); err != nil {
			return ListResultMsg{Err: err}
		}
		listed, err := c.gateway.ListJobs(ctx)
		return ListResultMsg{Jobs: listed, Err: err}
	}
}

// CreateCmd asynchronously creates a durable job. When the caller omits a
// JobID, the command allocates one before returning. The captured command can
// therefore be executed again without accidentally creating a second job,
// while gatewayclient can safely retry a lost acknowledgement.
func (c Client) CreateCmd(ctx context.Context, request gatewayapi.CreateJobRequest) tea.Cmd {
	request = cloneCreateRequest(request)
	if strings.TrimSpace(request.JobID) == "" {
		request.JobID = newClientJobID()
	}
	return func() tea.Msg {
		message := JobResultMsg{Action: ActionCreate, JobID: request.JobID}
		if err := c.ready(ctx); err != nil {
			message.Err = err
			return message
		}
		message.Response, message.Err = c.gateway.CreateJob(ctx, request)
		return message
	}
}

// ShowCmd asynchronously fetches the canonical durable state for one job.
func (c Client) ShowCmd(ctx context.Context, jobID string) tea.Cmd {
	return c.actionCmd(ctx, ActionShow, jobID, c.getJob)
}

// RunCmd asynchronously starts a queued job.
func (c Client) RunCmd(ctx context.Context, jobID string) tea.Cmd {
	return c.actionCmd(ctx, ActionRun, jobID, c.runJob)
}

// PauseCmd asynchronously requests a pause at the runtime's safe boundary.
func (c Client) PauseCmd(ctx context.Context, jobID string) tea.Cmd {
	return c.actionCmd(ctx, ActionPause, jobID, c.pauseJob)
}

// ResumeCmd asynchronously resumes a paused or waiting job.
func (c Client) ResumeCmd(ctx context.Context, jobID string) tea.Cmd {
	return c.actionCmd(ctx, ActionResume, jobID, c.resumeJob)
}

// CancelCmd asynchronously requests durable cancellation.
func (c Client) CancelCmd(ctx context.Context, jobID string) tea.Cmd {
	return c.actionCmd(ctx, ActionCancel, jobID, c.cancelJob)
}

type jobCall func(context.Context, string) (gatewayapi.JobResponse, error)

func (c Client) actionCmd(ctx context.Context, action Action, jobID string, call jobCall) tea.Cmd {
	jobID = strings.TrimSpace(jobID)
	return func() tea.Msg {
		message := JobResultMsg{Action: action, JobID: jobID}
		if err := c.ready(ctx); err != nil {
			message.Err = err
			return message
		}
		if jobID == "" {
			message.Err = fmt.Errorf("job id is required")
			return message
		}
		message.Response, message.Err = call(ctx, jobID)
		return message
	}
}

func (c Client) ready(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("job command context is nil")
	}
	if c.gateway == nil {
		return fmt.Errorf("job gateway client is nil")
	}
	return nil
}

func (c Client) getJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.gateway.GetJob(ctx, jobID)
}

func (c Client) runJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.gateway.RunJob(ctx, jobID)
}

func (c Client) pauseJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.gateway.PauseJob(ctx, jobID)
}

func (c Client) resumeJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.gateway.ResumeJob(ctx, jobID)
}

func (c Client) cancelJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.gateway.CancelJob(ctx, jobID)
}

func cloneCreateRequest(request gatewayapi.CreateJobRequest) gatewayapi.CreateJobRequest {
	if request.Deadline != nil {
		deadline := *request.Deadline
		request.Deadline = &deadline
	}
	request.Authority = jobs.Authority{
		Mode:         request.Authority.Mode,
		Tools:        append([]string(nil), request.Authority.Tools...),
		ReadRoots:    append([]string(nil), request.Authority.ReadRoots...),
		WriteRoots:   append([]string(nil), request.Authority.WriteRoots...),
		NetworkHosts: append([]string(nil), request.Authority.NetworkHosts...),
		Providers:    append([]string(nil), request.Authority.Providers...),
	}
	return request
}

var fallbackJobIDSequence atomic.Uint64

func newClientJobID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "j-" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("j-%x-%x", time.Now().UnixNano(), fallbackJobIDSequence.Add(1))
}
