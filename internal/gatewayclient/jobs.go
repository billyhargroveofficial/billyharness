package gatewayclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func (c *Client) CreateJob(ctx context.Context, request gatewayapi.CreateJobRequest) (gatewayapi.JobResponse, error) {
	if err := request.Validate(); err != nil {
		return gatewayapi.JobResponse{}, fmt.Errorf("create job request: %w", err)
	}
	attempts := 1
	if request.JobID != "" {
		// A client ID makes an ambiguous transport/decode failure safe to retry:
		// the gateway will either create once or return the durable winner.
		attempts = 2
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		var out gatewayapi.JobResponse
		lastErr = c.JSON(ctx, http.MethodPost, "/v1/jobs", request, &out)
		if lastErr == nil {
			return out, nil
		}
		var status *StatusError
		if errors.As(lastErr, &status) || ctx.Err() != nil {
			break
		}
	}
	return gatewayapi.JobResponse{}, lastErr
}

func (c *Client) ListJobs(ctx context.Context) ([]gatewayapi.JobSummaryResponse, error) {
	const pageSize = 200
	var jobs []gatewayapi.JobSummaryResponse
	for offset := 0; ; {
		var out gatewayapi.JobListResponse
		path := fmt.Sprintf("/v1/jobs?offset=%d&limit=%d", offset, pageSize)
		if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		jobs = append(jobs, out.Jobs...)
		if out.NextOffset == nil {
			return jobs, nil
		}
		if *out.NextOffset <= offset {
			return nil, fmt.Errorf("gateway returned non-advancing jobs page offset %d", *out.NextOffset)
		}
		offset = *out.NextOffset
	}
}

func (c *Client) GetJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.jobRequest(ctx, http.MethodGet, jobPath(jobID), nil)
}

func (c *Client) RunJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.jobAction(ctx, jobID, "run")
}

func (c *Client) PauseJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.jobAction(ctx, jobID, "pause")
}

func (c *Client) ResumeJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.jobAction(ctx, jobID, "resume")
}

func (c *Client) CancelJob(ctx context.Context, jobID string) (gatewayapi.JobResponse, error) {
	return c.jobAction(ctx, jobID, "cancel")
}

func (c *Client) ListJobAttempts(ctx context.Context, jobID string, offset, limit int) (gatewayapi.JobAttemptPage, error) {
	var out gatewayapi.JobAttemptPage
	path := fmt.Sprintf("%s/attempts?offset=%d&limit=%d", jobPath(jobID), offset, limit)
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return gatewayapi.JobAttemptPage{}, err
	}
	return out, nil
}

func (c *Client) ListJobArtifacts(ctx context.Context, jobID string, offset, limit int) (gatewayapi.JobArtifactPage, error) {
	var out gatewayapi.JobArtifactPage
	path := fmt.Sprintf("%s/artifacts?offset=%d&limit=%d", jobPath(jobID), offset, limit)
	if err := c.JSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return gatewayapi.JobArtifactPage{}, err
	}
	return out, nil
}

func (c *Client) jobAction(ctx context.Context, jobID, action string) (gatewayapi.JobResponse, error) {
	// The empty JSON object keeps mutation requests compatible with the
	// gateway's application/json CSRF boundary without adding action inputs.
	return c.jobRequest(ctx, http.MethodPost, jobPath(jobID)+"/"+action, struct{}{})
}

func (c *Client) jobRequest(ctx context.Context, method, path string, body any) (gatewayapi.JobResponse, error) {
	var out gatewayapi.JobResponse
	if err := c.JSON(ctx, method, path, body, &out); err != nil {
		return gatewayapi.JobResponse{}, err
	}
	return out, nil
}

func jobPath(jobID string) string {
	return "/v1/jobs/" + url.PathEscape(strings.TrimSpace(jobID))
}
