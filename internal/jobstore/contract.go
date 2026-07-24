// Package jobstore defines durable storage for provider-neutral jobs.
package jobstore

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

const (
	DefaultMaxRecordBytes   = 4 << 20
	DefaultMaxArtifactBytes = int64(256 << 20)
)

// Store is the persistence boundary for a durable job. Implementations own
// compare-and-append, replay, artifact verification, and process ownership.
type Store interface {
	// CoordinationKey returns an opaque, stable identity for the underlying
	// persistence namespace. Independent stores must return different keys;
	// decorators over the same store must preserve the same key. The runtime
	// uses it only for in-process job ownership and never persists or exposes it.
	CoordinationKey() string
	// ProtectedRoots returns canonical filesystem roots owned by the store.
	// Callers must treat them as denied authority boundaries. Implementations
	// return fresh slices so callers cannot mutate store-owned state.
	ProtectedRoots() []string
	Create(context.Context, jobs.JobSpec) (jobs.JobState, error)
	Append(context.Context, string, uint64, jobs.Event) (jobs.JobState, error)
	Load(context.Context, string) (jobs.JobState, error)
	// List validates jobs independently. Implementations report a per-job
	// failure through JobSummary.Quarantine and reserve the returned error for
	// failures which prevent listing the store as a whole.
	List(context.Context) ([]JobSummary, error)
	PutArtifact(context.Context, string, string, string, string, io.Reader) (jobs.ArtifactRef, error)
	OpenArtifact(context.Context, string, string) (io.ReadCloser, jobs.ArtifactRef, error)
	Close() error
}

// Options contains hard input limits. Zero selects the corresponding default;
// negative values are rejected.
type Options struct {
	MaxRecordBytes   int   `json:"max_record_bytes,omitempty"`
	MaxArtifactBytes int64 `json:"max_artifact_bytes,omitempty"`
}

func (o Options) Resolve() (Options, error) {
	if o.MaxRecordBytes < 0 {
		return Options{}, fmt.Errorf("max_record_bytes must not be negative")
	}
	if o.MaxArtifactBytes < 0 {
		return Options{}, fmt.Errorf("max_artifact_bytes must not be negative")
	}
	if o.MaxRecordBytes == 0 {
		o.MaxRecordBytes = DefaultMaxRecordBytes
	}
	if o.MaxArtifactBytes == 0 {
		o.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	return o, nil
}

func (o Options) Validate() error {
	_, err := o.Resolve()
	return err
}

type JobSummary struct {
	ID             string              `json:"id"`
	Goal           string              `json:"goal"`
	Preset         string              `json:"preset"`
	Status         jobs.JobStatus      `json:"status"`
	TerminalReason jobs.TerminalReason `json:"terminal_reason,omitempty"`
	Revision       uint64              `json:"revision"`
	Cycle          uint64              `json:"cycle"`
	Usage          jobs.Usage          `json:"usage"`
	Deadline       time.Time           `json:"deadline"`
	// Quarantine is set when this job failed closed during independent list
	// validation. A quarantined entry is never admitted for execution, but it
	// remains visible to operators so one damaged job cannot either hide itself
	// or make healthy jobs unavailable.
	Quarantine *QuarantineReport `json:"quarantine,omitempty"`
}

// QuarantineReport is deliberately bounded and path-free: it carries enough
// structured information to locate a corrupt record without exposing the
// server's filesystem layout or echoing potentially hostile file contents.
type QuarantineReport struct {
	Kind   CorruptionKind `json:"kind"`
	Line   int            `json:"line,omitempty"`
	Seq    uint64         `json:"seq,omitempty"`
	Offset int64          `json:"offset,omitempty"`
}

func (q QuarantineReport) String() string {
	detail := fmt.Sprintf("quarantined: corruption kind=%s", q.Kind)
	if q.Line > 0 {
		detail += fmt.Sprintf(" line=%d", q.Line)
	}
	if q.Seq > 0 {
		detail += fmt.Sprintf(" seq=%d", q.Seq)
	}
	if q.Offset > 0 {
		detail += fmt.Sprintf(" offset=%d", q.Offset)
	}
	return detail
}

// ValidatePortableID rejects values which could affect storage paths. Its
// grammar is stricter than the in-memory domain grammar because the value is
// used as a directory component on every supported operating system.
func ValidatePortableID(value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 || strings.HasSuffix(value, ".") {
		return &InvalidIDError{Value: value}
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9') ||
			(i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return &InvalidIDError{Value: value}
	}
	base := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	if windowsReservedBasename(base) {
		return &InvalidIDError{Value: value}
	}
	return nil
}

func windowsReservedBasename(base string) bool {
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	if len(base) == 4 && base[3] >= '1' && base[3] <= '9' {
		return base[:3] == "COM" || base[:3] == "LPT"
	}
	return false
}
