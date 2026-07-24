package jobstore

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreProtectedRootsContractIsCanonicalImmutableAndAvailableAfterClose(t *testing.T) {
	t.Parallel()

	requestedRoot := filepath.Join(t.TempDir(), "jobs")
	store, err := NewFileStore(requestedRoot, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var contract Store = store

	absolute, err := filepath.Abs(requestedRoot)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Clean(want)

	first := contract.ProtectedRoots()
	if !reflect.DeepEqual(first, []string{want}) {
		t.Fatalf("ProtectedRoots() = %v, want [%q]", first, want)
	}
	first[0] = filepath.Join(want, "mutated")
	if got := contract.ProtectedRoots(); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("caller mutated protected roots: %v", got)
	}
	if err := contract.Close(); err != nil {
		t.Fatal(err)
	}
	if got := contract.ProtectedRoots(); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("ProtectedRoots() after Close = %v, want [%q]", got, want)
	}
	if got := contract.CoordinationKey(); got != "file:"+want {
		t.Fatalf("CoordinationKey() after Close = %q, want %q", got, "file:"+want)
	}

	var nilStore *FileStore
	if got := nilStore.ProtectedRoots(); got != nil {
		t.Fatalf("nil FileStore ProtectedRoots() = %v, want nil", got)
	}
	if got := nilStore.CoordinationKey(); got != "" {
		t.Fatalf("nil FileStore CoordinationKey() = %q, want empty", got)
	}
}

func TestOptionsResolveDefaultsAndRejectsNegativeLimits(t *testing.T) {
	t.Parallel()

	got, err := (Options{}).Resolve()
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}
	want := Options{
		MaxRecordBytes:   DefaultMaxRecordBytes,
		MaxArtifactBytes: DefaultMaxArtifactBytes,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved options = %#v, want %#v", got, want)
	}
	for name, options := range map[string]Options{
		"record":   {MaxRecordBytes: -1},
		"artifact": {MaxArtifactBytes: -1},
	} {
		name, options := name, options
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := options.Resolve(); err == nil {
				t.Fatal("Resolve() succeeded for negative limit")
			}
		})
	}
}

func TestValidatePortableIDRejectsPathAndUnicodeInputs(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"job-1", "artifact_2", "control.reducer", "com10.report"} {
		if err := ValidatePortableID(valid); err != nil {
			t.Fatalf("ValidatePortableID(%q): %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"", "../job", "/absolute", "a/b", " job", "job ", ".hidden", "задача",
		"scope:child", "job.", "CON", "nul.txt", "Com1", "lpt9.log",
	} {
		err := ValidatePortableID(invalid)
		if !errors.Is(err, ErrInvalidID) {
			t.Fatalf("ValidatePortableID(%q) error = %v, want ErrInvalidID", invalid, err)
		}
	}
}

func TestTypedErrorsSupportErrorsIsAndMetadata(t *testing.T) {
	t.Parallel()

	conflict := &ConflictError{JobID: "job-1", ExpectedRevision: 3, ActualRevision: 4}
	if !errors.Is(conflict, ErrConflict) {
		t.Fatal("ConflictError does not match ErrConflict")
	}

	cause := errors.New("digest differs")
	corrupt := NewCorruptionError(CorruptionMetadata{
		JobID:  "job-1",
		Path:   "events.jsonl",
		Line:   2,
		Seq:    2,
		Offset: 128,
		Kind:   CorruptionHashMismatch,
	}, cause)
	if !errors.Is(corrupt, ErrCorrupt) || !errors.Is(corrupt, ErrTampered) || !errors.Is(corrupt, cause) {
		t.Fatalf("CorruptionError chain does not expose sentinels/cause: %v", corrupt)
	}
	var metadataError *CorruptionError
	if !errors.As(corrupt, &metadataError) || metadataError.Seq != 2 || metadataError.Offset != 128 {
		t.Fatalf("CorruptionError metadata = %#v", metadataError)
	}

	tooLarge := &TooLargeError{Resource: "artifact", Limit: 10, Actual: 11}
	if !errors.Is(tooLarge, ErrTooLarge) {
		t.Fatal("TooLargeError does not match ErrTooLarge")
	}
	ownership := &OwnershipError{Root: "/jobs", Err: cause}
	if !errors.Is(ownership, ErrOwnership) || !errors.Is(ownership, cause) {
		t.Fatal("OwnershipError does not expose ownership sentinel and cause")
	}
	committed := &CommitError{Operation: "create", JobID: "job-1", Revision: 2, Err: cause}
	if !errors.Is(committed, ErrCommitted) || !errors.Is(committed, cause) {
		t.Fatal("CommitError does not expose committed sentinel and cause")
	}
}
