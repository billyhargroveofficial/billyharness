package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/jobs"
	"github.com/billyhargroveofficial/billyharness/internal/jobservice"
	"github.com/billyhargroveofficial/billyharness/internal/jobstore"
)

func createJobRequestHash(request gatewayapi.CreateJobRequest) (string, error) {
	canonical, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal canonical create request: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// existingIdempotentCreate checks the immutable request binding before normal
// admission-time validation. This is what lets an identical retry retrieve its
// durable winner even after an explicit absolute deadline has elapsed. A new
// job with that elapsed deadline still reaches compileJobSpec and is rejected.
func existingIdempotentCreate(
	ctx context.Context,
	controller JobController,
	request gatewayapi.CreateJobRequest,
) (jobservice.View, bool, error) {
	if request.JobID == "" {
		return jobservice.View{}, false, nil
	}
	if err := jobstore.ValidatePortableID(request.JobID); err != nil {
		// Preserve the normal admission error and avoid passing an invalid path
		// component to a controller implementation.
		return jobservice.View{}, false, nil
	}
	wantHash, err := createJobRequestHash(request)
	if err != nil {
		return jobservice.View{}, false, err
	}
	view, err := controller.Get(ctx, request.JobID)
	if errors.Is(err, jobstore.ErrNotFound) {
		return jobservice.View{}, false, nil
	}
	if err != nil {
		return jobservice.View{}, false, err
	}
	gotHash := view.State.Spec.CreateRequestHash
	if gotHash == "" || subtle.ConstantTimeCompare([]byte(gotHash), []byte(wantHash)) != 1 {
		return jobservice.View{}, false, &jobservice.CreateConflictError{JobID: request.JobID}
	}
	return view, true, nil
}

func autoStartCreatedJob(
	ctx context.Context,
	controller JobController,
	jobID string,
	view jobservice.View,
) (jobservice.View, error) {
	if view.State.Status != jobs.JobStatusQueued && view.State.Status != jobs.JobStatusRunning {
		return view, nil
	}
	started, err := controller.Start(ctx, jobID)
	if err == nil {
		return started, nil
	}
	// A duplicate auto-start can race the durable runner from RUNNING to a
	// dormant/terminal state. Returning that canonical state is the idempotent
	// create result; it must not become a spurious conflict.
	if errors.Is(err, jobservice.ErrNotStartable) {
		latest, loadErr := controller.Get(ctx, jobID)
		if loadErr == nil && latest.State.Status != jobs.JobStatusQueued && latest.State.Status != jobs.JobStatusRunning {
			return latest, nil
		}
	}
	return jobservice.View{}, err
}
