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
	Create(context.Context, jobs.JobSpec) (jobs.JobState, error)
	Append(context.Context, string, uint64, jobs.Event) (jobs.JobState, error)
	Load(context.Context, string) (jobs.JobState, error)
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
