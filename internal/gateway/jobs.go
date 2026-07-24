package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobservice"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const (
	maxCreateJobRequestBodyBytes int64 = 4 << 20
	maxJobActionRequestBodyBytes int64 = 1 << 10
	defaultJobListPageSize             = 100
	maxJobListPageSize                 = 500
	defaultJobAttemptPageSize          = 10
	maxJobAttemptPageSize              = 32
	defaultJobArtifactPageSize         = 100
	maxJobArtifactPageSize             = 500
	maxJobSummaryGoalRunes             = 512
)

var errJobRequestBodyTooLarge = errors.New("job request body too large")

// JobController is the durable execution surface used by the HTTP gateway.
// *jobservice.Manager satisfies it directly; keeping it narrow prevents the
// gateway from reaching into a store or runtime implementation.
type JobController interface {
	Create(context.Context, jobs.JobSpec) (jobservice.View, error)
	CreateIdempotent(context.Context, jobs.JobSpec) (jobservice.View, error)
	Get(context.Context, string) (jobservice.View, error)
	List(context.Context) ([]jobservice.Summary, error)
	Start(context.Context, string) (jobservice.View, error)
	Pause(context.Context, string) (jobservice.View, error)
	Resume(context.Context, string) (jobservice.View, error)
	Cancel(context.Context, string) (jobservice.View, error)
}

type strictJSONObjectSchema map[string]*strictJSONObjectSchema

var createJobRequestSchema = strictJSONObjectSchema{
	"job_id":              nil,
	"goal":                nil,
	"preset":              nil,
	"workers":             nil,
	"min_cycles":          nil,
	"route":               schemaPtr(strictJSONObjectSchema{"provider_id": nil, "model_id": nil, "thinking": nil, "reasoning_effort": nil}),
	"duration_seconds":    nil,
	"deadline":            nil,
	"min_runtime_seconds": nil,
	"cadence_seconds":     nil,
	"budget":              schemaPtr(strictJSONObjectSchema{"max_cycles": nil, "max_attempts": nil, "max_model_calls": nil, "max_tokens": nil}),
	"authority": schemaPtr(strictJSONObjectSchema{
		"mode": nil, "tools": nil, "read_roots": nil, "write_roots": nil, "network_hosts": nil, "providers": nil,
	}),
	"auto_start": nil,
}

func schemaPtr(schema strictJSONObjectSchema) *strictJSONObjectSchema { return &schema }

func cloneValidJobAuthority(authority jobs.Authority) jobs.Authority {
	if err := authority.Validate(); err != nil {
		return jobs.DenyAllAuthority()
	}
	canonical, err := canonicalizeAuthorityProviders(authority)
	if err != nil {
		return jobs.DenyAllAuthority()
	}
	return canonical
}

func cloneJobAuthority(authority jobs.Authority) jobs.Authority {
	authority.Tools = append([]string(nil), authority.Tools...)
	authority.ReadRoots = append([]string(nil), authority.ReadRoots...)
	authority.WriteRoots = append([]string(nil), authority.WriteRoots...)
	authority.NetworkHosts = append([]string(nil), authority.NetworkHosts...)
	authority.Providers = append([]string(nil), authority.Providers...)
	return authority
}

func canonicalizeAuthorityProviders(authority jobs.Authority) (jobs.Authority, error) {
	authority = cloneJobAuthority(authority)
	if authority.Mode == jobs.AuthorityModeDenyAll {
		return authority, nil
	}
	seen := make(map[string]struct{}, len(authority.Providers))
	for index, providerID := range authority.Providers {
		canonical := providerID
		if providerID != "*" {
			canonical = modelinfo.NormalizeProvider(providerID)
		}
		if canonical == "" {
			return jobs.DenyAllAuthority(), errors.New("authority provider grant is empty after normalization")
		}
		if _, duplicate := seen[canonical]; duplicate {
			return jobs.DenyAllAuthority(), fmt.Errorf("authority provider grants repeat canonical provider %q", canonical)
		}
		seen[canonical] = struct{}{}
		authority.Providers[index] = canonical
	}
	return authority, nil
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireJobController(w)
	if !ok {
		return
	}
	req, err := decodeCreateJobRequest(r.Body)
	if err != nil {
		if errors.Is(err, errJobRequestBodyTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid job request JSON: "+err.Error())
		return
	}
	existing, found, err := existingIdempotentCreate(r.Context(), controller, req)
	if err != nil {
		writeJobError(w, "reconcile create", err)
		return
	}
	if found {
		if req.AutoStart {
			existing, err = autoStartCreatedJob(r.Context(), controller, req.JobID, existing)
			if err != nil {
				writeJobError(w, "start after create", err)
				return
			}
		}
		writeJSON(w, http.StatusCreated, jobResponse(existing))
		return
	}
	spec, err := s.compileJobSpec(req, time.Now().UTC())
	if err != nil {
		writeJobError(w, "compile", err)
		return
	}
	var view jobservice.View
	if req.JobID == "" {
		view, err = controller.Create(r.Context(), spec)
	} else {
		view, err = controller.CreateIdempotent(r.Context(), spec)
	}
	if err != nil {
		writeJobError(w, "create", err)
		return
	}
	if req.AutoStart {
		view, err = autoStartCreatedJob(r.Context(), controller, spec.ID, view)
		if err != nil {
			writeJobError(w, "start after create", err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, jobResponse(view))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireJobController(w)
	if !ok {
		return
	}
	summaries, err := controller.List(r.Context())
	if err != nil {
		writeJobError(w, "list", err)
		return
	}
	offset, limit, err := jobPageParams(r, defaultJobListPageSize, maxJobListPageSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	start, end, next := jobPageBounds(len(summaries), offset, limit)
	response := gatewayapi.JobListResponse{
		Jobs:       make([]gatewayapi.JobSummaryResponse, 0, end-start),
		Offset:     start,
		Limit:      limit,
		Total:      len(summaries),
		NextOffset: next,
	}
	for _, summary := range summaries[start:end] {
		response.Jobs = append(response.Jobs, jobSummaryResponse(summary))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireJobController(w)
	if !ok {
		return
	}
	jobID, ok := validatedJobID(w, r)
	if !ok {
		return
	}
	view, err := controller.Get(r.Context(), jobID)
	if err != nil {
		writeJobError(w, "get", err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponse(view))
}

func (s *Server) handleListJobAttempts(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireJobController(w)
	if !ok {
		return
	}
	jobID, ok := validatedJobID(w, r)
	if !ok {
		return
	}
	offset, limit, err := jobPageParams(r, defaultJobAttemptPageSize, maxJobAttemptPageSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := controller.Get(r.Context(), jobID)
	if err != nil {
		writeJobError(w, "list attempts", err)
		return
	}
	start, end, next := jobPageBounds(len(view.State.Attempts), offset, limit)
	attempts := append([]jobs.Attempt(nil), view.State.Attempts[start:end]...)
	writeJSON(w, http.StatusOK, gatewayapi.JobAttemptPage{
		JobID: jobID, Offset: start, Limit: limit, Total: len(view.State.Attempts), NextOffset: next, Attempts: attempts,
	})
}

func (s *Server) handleListJobArtifacts(w http.ResponseWriter, r *http.Request) {
	controller, ok := s.requireJobController(w)
	if !ok {
		return
	}
	jobID, ok := validatedJobID(w, r)
	if !ok {
		return
	}
	offset, limit, err := jobPageParams(r, defaultJobArtifactPageSize, maxJobArtifactPageSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := controller.Get(r.Context(), jobID)
	if err != nil {
		writeJobError(w, "list artifacts", err)
		return
	}
	start, end, next := jobPageBounds(len(view.State.Artifacts), offset, limit)
	artifacts := append([]jobs.ArtifactRef(nil), view.State.Artifacts[start:end]...)
	writeJSON(w, http.StatusOK, gatewayapi.JobArtifactPage{
		JobID: jobID, Offset: start, Limit: limit, Total: len(view.State.Artifacts), NextOffset: next, Artifacts: artifacts,
	})
}

func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	s.handleJobAction(w, r, "run", func(ctx context.Context, controller JobController, jobID string) (jobservice.View, error) {
		return controller.Start(ctx, jobID)
	})
}

func (s *Server) handlePauseJob(w http.ResponseWriter, r *http.Request) {
	s.handleJobAction(w, r, "pause", func(ctx context.Context, controller JobController, jobID string) (jobservice.View, error) {
		return controller.Pause(ctx, jobID)
	})
}

func (s *Server) handleResumeJob(w http.ResponseWriter, r *http.Request) {
	s.handleJobAction(w, r, "resume", func(ctx context.Context, controller JobController, jobID string) (jobservice.View, error) {
		return controller.Resume(ctx, jobID)
	})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	s.handleJobAction(w, r, "cancel", func(ctx context.Context, controller JobController, jobID string) (jobservice.View, error) {
		return controller.Cancel(ctx, jobID)
	})
}

func (s *Server) handleJobAction(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
	call func(context.Context, JobController, string) (jobservice.View, error),
) {
	controller, ok := s.requireJobController(w)
	if !ok {
		return
	}
	jobID, ok := validatedJobID(w, r)
	if !ok {
		return
	}
	if err := decodeEmptyJobAction(r.Body); err != nil {
		if errors.Is(err, errJobRequestBodyTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid job action JSON: "+err.Error())
		return
	}
	view, err := call(r.Context(), controller, jobID)
	if err != nil {
		writeJobError(w, operation, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponse(view))
}

func (s *Server) requireJobController(w http.ResponseWriter) (JobController, bool) {
	if s == nil || s.jobController == nil {
		writeError(w, http.StatusServiceUnavailable, "job service is unavailable")
		return nil, false
	}
	return s.jobController, true
}

func validatedJobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	jobID := r.PathValue("id")
	if err := jobstore.ValidatePortableID(jobID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return "", false
	}
	return jobID, true
}

func decodeCreateJobRequest(body io.Reader) (gatewayapi.CreateJobRequest, error) {
	raw, err := readBoundedJobBody(body, maxCreateJobRequestBodyBytes)
	if err != nil {
		return gatewayapi.CreateJobRequest{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return gatewayapi.CreateJobRequest{}, errors.New("request body is required")
	}
	if err := inspectStrictJSONObject(raw, createJobRequestSchema); err != nil {
		return gatewayapi.CreateJobRequest{}, err
	}
	var request gatewayapi.CreateJobRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return gatewayapi.CreateJobRequest{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return gatewayapi.CreateJobRequest{}, err
	}
	return request, nil
}

func decodeEmptyJobAction(body io.Reader) error {
	if body == nil {
		return nil
	}
	raw, err := readBoundedJobBody(body, maxJobActionRequestBodyBytes)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return inspectStrictJSONObject(raw, strictJSONObjectSchema{})
}

func readBoundedJobBody(body io.Reader, limit int64) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: maximum is %d bytes", errJobRequestBodyTooLarge, limit)
	}
	return raw, nil
}

func inspectStrictJSONObject(raw []byte, schema strictJSONObjectSchema) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := consumeStrictJSONObject(decoder, schema); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func consumeStrictJSONObject(decoder *json.Decoder, schema strictJSONObjectSchema) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return errors.New("request body must be a JSON object")
	}
	seen := make(map[string]struct{}, len(schema))
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("request field name must be a string")
		}
		child, known := schema[key]
		if !known {
			return fmt.Errorf("unknown JSON field %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate JSON field %q", key)
		}
		seen[key] = struct{}{}
		if child != nil {
			if err := consumeStrictJSONObject(decoder, *child); err != nil {
				return fmt.Errorf("field %q: %w", key, err)
			}
			continue
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain exactly one JSON object")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

type jobAdmissionError struct {
	status  int
	message string
}

func (e *jobAdmissionError) Error() string { return e.message }

func (s *Server) compileJobSpec(request gatewayapi.CreateJobRequest, admittedAt time.Time) (jobs.JobSpec, error) {
	deadline, notBeforeComplete, err := request.ResolveSchedule(admittedAt)
	if err != nil {
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: err.Error()}
	}
	workflow, err := jobs.CompilePreset(request.Preset, request.Workers)
	if err != nil {
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: err.Error()}
	}
	route := request.Route
	route.ProviderID = modelinfo.NormalizeProvider(route.ProviderID)
	route.ModelID = modelinfo.NormalizeAlias(route.ModelID)
	if err := route.Validate(); err != nil {
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: "route: " + err.Error()}
	}
	if resolvedProvider := modelinfo.ProviderForModel(route.ModelID, route.ProviderID); resolvedProvider != route.ProviderID {
		return jobs.JobSpec{}, &jobAdmissionError{
			status: http.StatusBadRequest,
			message: fmt.Sprintf(
				"provider/model conflict: model %q resolves to provider %q, not %q",
				route.ModelID,
				resolvedProvider,
				route.ProviderID,
			),
		}
	}
	requestedAuthority, err := canonicalizeAuthorityProviders(request.Authority)
	if err != nil {
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: "invalid authority: " + err.Error()}
	}
	authority, err := jobs.IntersectAuthority(s.jobAuthority, requestedAuthority)
	if err != nil {
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: "invalid authority: " + err.Error()}
	}
	if !jobAuthorityAllowsProvider(authority, route.ProviderID) {
		return jobs.JobSpec{}, &jobAdmissionError{
			status:  http.StatusForbidden,
			message: "requested authority does not permit the execution route provider within the server authority",
		}
	}
	if err := modelinfo.ValidateCapabilityPolicy(modelinfo.CapabilityPolicyRequest{
		Provider:           route.ProviderID,
		Model:              route.ModelID,
		Thinking:           route.Thinking,
		ReasoningEffort:    route.ReasoningEffort,
		RequireToolCalls:   route.ProviderID != modelinfo.ProviderMock,
		RequireStreaming:   true,
		AllowUnknownModels: modelinfo.Provider(route.ProviderID).Custom,
	}); err != nil {
		// This mirrors provider.NewFromBinding: non-mock chat transports must
		// support the agent tool-call protocol even when this particular job has
		// provider-only authority. Output is intentionally not checked here:
		// jobagent.pinBinding clamps each reservation to the advertised ceiling.
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: "unsupported execution route: " + err.Error()}
	}
	// Persist a concrete route provider even when either input used a wildcard.
	// A resumed job must never silently select a newly available provider.
	authority.Providers = []string{route.ProviderID}
	roles := make([]jobs.RoleSpec, len(workflow.Roles))
	for index, template := range workflow.Roles {
		roleAuthority := cloneJobAuthority(authority)
		if !template.Writer {
			// Only the preset's isolated writer role may inherit filesystem
			// mutation authority. Readers and both control roles stay read-only.
			roleAuthority.WriteRoots = nil
		}
		template.Authority = roleAuthority
		roles[index] = template
		if !jobAuthorityAllowsProvider(template.Authority, route.ProviderID) {
			return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusForbidden, message: "execution route provider is not allowed for every workflow role"}
		}
	}
	jobID := request.JobID
	createRequestHash := ""
	if jobID == "" {
		jobID = newPortableJobID()
	} else {
		createRequestHash, err = createJobRequestHash(request)
		if err != nil {
			return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: err.Error()}
		}
	}
	spec := jobs.JobSpec{
		ID:                  jobID,
		Goal:                request.Goal,
		Preset:              request.Preset,
		Workers:             request.Workers,
		CreateRequestHash:   createRequestHash,
		MinCycles:           request.MinCycles,
		AdmittedAt:          admittedAt.UTC(),
		NotBeforeComplete:   notBeforeComplete,
		CycleCadenceSeconds: request.CadenceSeconds,
		Deadline:            deadline,
		Budget:              request.Budget,
		Route:               route,
		Workflow:            jobs.WorkflowControlFromWorkflow(workflow),
		Authority:           authority,
		Roles:               roles,
		Stages:              append([]jobs.StageSpec(nil), workflow.Stages...),
	}
	if err := jobstore.ValidatePortableID(spec.ID); err != nil {
		if request.JobID != "" {
			return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: "invalid job id"}
		}
		return jobs.JobSpec{}, errors.New("generate portable job id")
	}
	if err := spec.Validate(); err != nil {
		return jobs.JobSpec{}, &jobAdmissionError{status: http.StatusBadRequest, message: "invalid compiled job: " + err.Error()}
	}
	return spec, nil
}

func jobAuthorityAllowsProvider(authority jobs.Authority, providerID string) bool {
	if authority.Mode != jobs.AuthorityModeAllowList || providerID == "" || providerID == "*" {
		return false
	}
	for _, allowed := range authority.Providers {
		if allowed == "*" || allowed == providerID {
			return true
		}
	}
	return false
}

func newPortableJobID() string { return "j-" + newID() }

func jobResponse(view jobservice.View) gatewayapi.JobResponse {
	state := view.State
	history := gatewayapi.JobHistorySummary{
		Attempts:         uint64(len(state.Attempts)),
		CompletedBatches: uint64(len(state.CompletedBatches)),
		Artifacts:        uint64(len(state.Artifacts)),
	}
	// Audit history is intentionally paged. Keep the state/spec/control fields
	// needed by create/show/actions, including the canonical final_result.
	state.Attempts = nil
	state.CompletedBatches = nil
	state.Artifacts = nil
	return gatewayapi.JobResponse{
		State:     state,
		Active:    view.Active,
		LastError: strings.TrimSpace(secrets.Redact(view.LastError)),
		History:   history,
	}
}

func jobSummaryResponse(summary jobservice.Summary) gatewayapi.JobSummaryResponse {
	job := summary.Job
	return gatewayapi.JobSummaryResponse{
		ID:             job.ID,
		Goal:           truncateJobSummaryGoal(job.Goal),
		Preset:         job.Preset,
		Status:         job.Status,
		TerminalReason: job.TerminalReason,
		Revision:       job.Revision,
		Cycle:          job.Cycle,
		Usage:          job.Usage,
		AdmittedAt:     job.AdmittedAt,
		Deadline:       job.Deadline,
		Active:         summary.Active,
		LastError:      strings.TrimSpace(secrets.Redact(summary.LastError)),
	}
}

func truncateJobSummaryGoal(goal string) string {
	runes := []rune(strings.TrimSpace(goal))
	if len(runes) <= maxJobSummaryGoalRunes {
		return string(runes)
	}
	return string(runes[:maxJobSummaryGoalRunes]) + "…"
}

func jobPageParams(r *http.Request, defaultLimit, maxLimit int) (int, int, error) {
	parse := func(name string, fallback int) (int, error) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			return fallback, nil
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer", name)
		}
		return value, nil
	}
	offset, err := parse("offset", 0)
	if err != nil {
		return 0, 0, err
	}
	limit, err := parse("limit", defaultLimit)
	if err != nil {
		return 0, 0, err
	}
	if limit == 0 || limit > maxLimit {
		return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}
	return offset, limit, nil
}

func jobPageBounds(total, offset, limit int) (int, int, *int) {
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	if end >= total {
		return offset, end, nil
	}
	next := end
	return offset, end, &next
}

func writeJobError(w http.ResponseWriter, operation string, err error) {
	if err == nil {
		writeError(w, http.StatusInternalServerError, "job operation failed")
		return
	}
	var admission *jobAdmissionError
	switch {
	case errors.As(err, &admission):
		writeError(w, admission.status, admission.message)
	case errors.Is(err, jobstore.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "invalid job id")
	case errors.Is(err, jobstore.ErrCorrupt), errors.Is(err, jobstore.ErrTampered):
		writeError(w, http.StatusConflict, "job state is corrupt")
	case errors.Is(err, jobstore.ErrNotFound):
		writeError(w, http.StatusNotFound, "job not found")
	case errors.Is(err, jobservice.ErrCreateConflict):
		writeError(w, http.StatusConflict, "job id belongs to a different create request")
	case errors.Is(err, jobstore.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "job already exists")
	case errors.Is(err, jobstore.ErrConflict):
		writeError(w, http.StatusConflict, "job state changed; retry the operation")
	case errors.Is(err, jobstore.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "job value is too large")
	case errors.Is(err, jobservice.ErrClosed), errors.Is(err, jobstore.ErrClosed), errors.Is(err, jobstore.ErrOwnership):
		writeError(w, http.StatusServiceUnavailable, "job service is unavailable")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusRequestTimeout, "job operation cancelled")
	case errors.Is(err, jobservice.ErrNotStartable), errors.Is(err, jobservice.ErrNotControllable), errors.Is(err, jobservice.ErrPauseFailed):
		writeError(w, http.StatusConflict, secrets.Redact(err.Error()))
	default:
		log.Printf("gateway job operation failed operation=%s error=%s", operation, secrets.Redact(err.Error()))
		writeError(w, http.StatusInternalServerError, "job operation failed")
	}
}
