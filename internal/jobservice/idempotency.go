package jobservice

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

var ErrCreateConflict = errors.New("job create request conflicts with existing job")

// CreateConflictError reports reuse of a client-supplied job ID for a
// different immutable create request. It also matches jobstore.ErrAlreadyExists
// so callers which only understand strict create semantics still fail closed.
type CreateConflictError struct {
	JobID string
}

func (e *CreateConflictError) Error() string {
	if e == nil || e.JobID == "" {
		return ErrCreateConflict.Error()
	}
	return fmt.Sprintf("%s: %q", ErrCreateConflict, e.JobID)
}

func (e *CreateConflictError) Is(target error) bool {
	return target == ErrCreateConflict || target == jobstore.ErrAlreadyExists
}

// CreateIdempotent creates a job whose client ID and canonical create hash are
// already embedded in spec. Unlike Create, it reconciles a duplicate/lost ACK
// by loading the durable winner. Server-derived schedule timestamps and the
// admission timestamp may differ: relative fields are resolved at admission
// time on every HTTP attempt, while CreateRequestHash binds the original
// request itself.
func (m *Manager) CreateIdempotent(ctx context.Context, spec jobs.JobSpec) (View, error) {
	if err := m.requireOpen(); err != nil {
		return View{}, err
	}
	if spec.CreateRequestHash == "" {
		return View{}, errors.New("idempotent create requires create_request_hash")
	}
	if err := spec.Validate(); err != nil {
		return View{}, fmt.Errorf("validate idempotent job spec: %w", err)
	}

	state, err := m.store.Create(ctx, spec)
	if err == nil {
		return m.annotate(state), nil
	}
	if !errors.Is(err, jobstore.ErrAlreadyExists) && !errors.Is(err, jobstore.ErrCommitted) {
		return View{}, err
	}

	existing, loadErr := m.store.Load(ctx, spec.ID)
	if loadErr != nil {
		return View{}, errors.Join(err, fmt.Errorf("reconcile idempotent create: %w", loadErr))
	}
	if !equivalentIdempotentCreate(existing.Spec, spec) {
		return View{}, &CreateConflictError{JobID: spec.ID}
	}
	return m.annotate(existing), nil
}

func equivalentIdempotentCreate(existing, requested jobs.JobSpec) bool {
	if existing.ID != requested.ID || existing.CreateRequestHash == "" || requested.CreateRequestHash == "" ||
		subtle.ConstantTimeCompare([]byte(existing.CreateRequestHash), []byte(requested.CreateRequestHash)) != 1 {
		return false
	}
	// These absolute times are admission-clock products. The request hash still
	// distinguishes relative fields from explicit timestamps and binds their
	// exact values, so normalizing here cannot merge distinct requests.
	requested.Deadline = existing.Deadline
	requested.NotBeforeComplete = existing.NotBeforeComplete
	requested.AdmittedAt = existing.AdmittedAt
	existingEnvelope, err := jobstore.NewSpecEnvelope(existing)
	if err != nil {
		return false
	}
	requestedEnvelope, err := jobstore.NewSpecEnvelope(requested)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(existingEnvelope.SpecHash), []byte(requestedEnvelope.SpecHash)) == 1
}
