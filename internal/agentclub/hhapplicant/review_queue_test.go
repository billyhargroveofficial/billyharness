package hhapplicant

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCaptureBuildsReadOnlyIngressEvent(t *testing.T) {
	repo := t.TempDir()
	stdout := []byte("Pending: 2 (показано 2)\n\n#12 neg=44 | Vacancy | class=other | reason\n    recruiter text\n\n#13 [done] neg=45 | Other | class=hr_contact | reason\n    contact text\n")
	var gotSpec CommandSpec
	adapter := Adapter{
		Runner: RunnerFunc(func(ctx context.Context, spec CommandSpec) (CommandResult, error) {
			gotSpec = spec
			return CommandResult{Stdout: stdout}, nil
		}),
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
		Timeout:          time.Second,
		OutputLimitBytes: 4096,
	}

	capture, err := adapter.Capture(context.Background(), ReviewQueueRequest{
		SessionID: "session-1",
		Profile:   "prod",
		Limit:     2,
		RepoRoot:  repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSpec.Name != ReviewQueueCommand || !reflect.DeepEqual(gotSpec.Args, []string{ReviewQueueSubcommand, "--limit", "2"}) {
		t.Fatalf("command spec = %#v", gotSpec)
	}
	if gotSpec.Dir != repo || gotSpec.OutputLimitBytes != 4096 || !reflect.DeepEqual(gotSpec.Env, []string{"HH_PROFILE_ID=prod"}) {
		t.Fatalf("command spec = %#v", gotSpec)
	}
	if capture.Owner.ClientType != "ingress" || capture.Owner.ClientID != "ingress:hh-applicant-tool:prod" {
		t.Fatalf("owner = %#v", capture.Owner)
	}
	if capture.Event.Source != ReviewQueueSource || capture.Rule.Source != ReviewQueueSource || capture.Rule.ID != ReviewQueueRuleID {
		t.Fatalf("event/rule = %#v %#v", capture.Event, capture.Rule)
	}
	if capture.Event.TargetSessionID != "session-1" || capture.Event.ExternalEventID == "" || capture.Event.Prompt == "" {
		t.Fatalf("event = %#v", capture.Event)
	}
	if !strings.Contains(capture.Event.Prompt, "recruiter text") || !strings.Contains(capture.Event.Prompt, "Do not mark entries done") {
		t.Fatalf("prompt = %q", capture.Event.Prompt)
	}
	if string(capture.Event.RawBody) != string(stdout) {
		t.Fatalf("raw body = %q", string(capture.Event.RawBody))
	}
	if capture.OutputSHA256 == "" || capture.Event.Metadata["hh.output_sha256"] != capture.OutputSHA256 {
		t.Fatalf("metadata/hash = %#v hash=%s", capture.Event.Metadata, capture.OutputSHA256)
	}
	if capture.ReviewItemCount != 2 || capture.Event.Metadata["hh.review_item_count"] != "2" {
		t.Fatalf("review item count = %d metadata=%#v", capture.ReviewItemCount, capture.Event.Metadata)
	}
	for _, forbidden := range []string{"cmd", "command", "argv", "provider", "model", "access_mode"} {
		if _, ok := capture.Event.Metadata[forbidden]; ok {
			t.Fatalf("unsafe metadata key %q in %#v", forbidden, capture.Event.Metadata)
		}
	}

	again, err := adapter.Capture(context.Background(), ReviewQueueRequest{
		SessionID: "session-1",
		Profile:   "prod",
		Limit:     2,
		RepoRoot:  repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ExternalEventID != capture.ExternalEventID {
		t.Fatalf("external event id changed: %q != %q", again.ExternalEventID, capture.ExternalEventID)
	}
	differentTarget, err := adapter.Capture(context.Background(), ReviewQueueRequest{
		SessionID: "session-2",
		Profile:   "prod",
		Limit:     2,
		RepoRoot:  repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if differentTarget.ExternalEventID == capture.ExternalEventID {
		t.Fatalf("external event id should include target session")
	}
}

func TestCaptureRejectsInvalidLimitProfileAndRepoWithoutRunner(t *testing.T) {
	repo := t.TempDir()
	calls := 0
	adapter := Adapter{
		Runner: RunnerFunc(func(context.Context, CommandSpec) (CommandResult, error) {
			calls++
			return CommandResult{}, nil
		}),
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
	}
	cases := []struct {
		name string
		req  ReviewQueueRequest
		want error
	}{
		{name: "missing profile", req: ReviewQueueRequest{SessionID: "session-1", Limit: 1, RepoRoot: repo}, want: ErrInvalidProfile},
		{name: "path profile", req: ReviewQueueRequest{SessionID: "session-1", Profile: "../prod", Limit: 1, RepoRoot: repo}, want: ErrInvalidProfile},
		{name: "zero limit", req: ReviewQueueRequest{SessionID: "session-1", Profile: "prod", RepoRoot: repo}, want: ErrInvalidLimit},
		{name: "too high limit", req: ReviewQueueRequest{SessionID: "session-1", Profile: "prod", Limit: MaxLimit + 1, RepoRoot: repo}, want: ErrInvalidLimit},
		{name: "unallowlisted repo", req: ReviewQueueRequest{SessionID: "session-1", Profile: "prod", Limit: 1, RepoRoot: t.TempDir()}, want: ErrInvalidRepoRoot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := adapter.Capture(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("runner called %d times for rejected requests", calls)
	}
}

func TestCaptureMapsTimeoutOutputCapAndCommandFailure(t *testing.T) {
	repo := t.TempDir()
	baseReq := ReviewQueueRequest{SessionID: "session-1", Profile: "prod", Limit: 1, RepoRoot: repo}

	timeoutAdapter := Adapter{
		Runner: RunnerFunc(func(ctx context.Context, spec CommandSpec) (CommandResult, error) {
			<-ctx.Done()
			return CommandResult{}, ctx.Err()
		}),
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
		Timeout:          time.Nanosecond,
		OutputLimitBytes: 1024,
	}
	if _, err := timeoutAdapter.Capture(context.Background(), baseReq); !errors.Is(err, ErrCommandTimeout) {
		t.Fatalf("timeout err = %v", err)
	}

	cappedAdapter := Adapter{
		Runner: RunnerFunc(func(context.Context, CommandSpec) (CommandResult, error) {
			return CommandResult{Stdout: []byte("too much"), StdoutTruncated: true}, nil
		}),
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
		OutputLimitBytes: 4,
	}
	if _, err := cappedAdapter.Capture(context.Background(), baseReq); !errors.Is(err, ErrOutputLimitExceeded) {
		t.Fatalf("cap err = %v", err)
	}

	failedAdapter := Adapter{
		Runner: RunnerFunc(func(context.Context, CommandSpec) (CommandResult, error) {
			return CommandResult{ExitCode: 2}, &CommandError{ExitCode: 2}
		}),
		RepoRoot:         repo,
		AllowedRepoRoots: []string{repo},
		OutputLimitBytes: 1024,
	}
	var commandErr *CommandError
	if _, err := failedAdapter.Capture(context.Background(), baseReq); !errors.As(err, &commandErr) || !errors.Is(err, ErrReviewCommandFailed) {
		t.Fatalf("command err = %T %[1]v", err)
	}
}
