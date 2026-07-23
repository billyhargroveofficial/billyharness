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
	closed    bool
}

var _ Store = (*FileStore)(nil)

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

	// A caller may not know whether the commit response was delivered. An
	// exact retry of the final persisted event is therefore successful, but a
	// reused ID with different content always conflicts.
	if loaded.replay.LastRecord != nil && loaded.replay.LastRecord.Event.ID == event.ID {
		last := loaded.replay.LastRecord
		if canonicalJSONEqual(last.Event, event) && expectedRevision == last.ExpectedRevision {
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
	for _, existing := range loaded.replay.Records {
		if existing.Event.ID == event.ID {
			return jobs.JobState{}, &ConflictError{
				JobID:            jobID,
				ExpectedRevision: expectedRevision,
				ActualRevision:   loaded.state.Revision,
			}
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
		loaded.replay.Seq+1,
		expectedRevision,
		next.Revision,
		loaded.replay.LastHash,
		event,
	)
	if err != nil {
		return jobs.JobState{}, fmt.Errorf("build event record: %w", err)
	}
	jobDir, err := s.jobPath(jobID)
	if err != nil {
		return jobs.JobState{}, err
	}
	eventsPath := filepath.Join(jobDir, eventsFileName)
	eventLogBytes, err := appendCanonicalEvent(eventsPath, record, s.options.MaxRecordBytes)
	if err != nil {
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
	if eventLogBytes <= maxSnapshotBytes/2 {
		_ = writeJSONAtomic(filepath.Join(jobDir, snapshotFileName), snapshot, maxSnapshotBytes)
	}
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
			return nil, NewCorruptionError(CorruptionMetadata{
				Path: filepath.Join(s.root, name),
				Kind: CorruptionIdentityMismatch,
			}, fmt.Errorf("invalid job directory name: %w", err))
		}
		if !entry.IsDir() {
			return nil, NewCorruptionError(CorruptionMetadata{
				JobID: name,
				Path:  filepath.Join(s.root, name),
				Kind:  CorruptionIdentityMismatch,
			}, errors.New("job entry is not a directory"))
		}
		loaded, err := s.loadLocked(ctx, name)
		if err != nil {
			return nil, err
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
	spec   SpecEnvelope
	state  jobs.JobState
	replay ReplayResult
}

func (s *FileStore) loadLocked(ctx context.Context, jobID string) (loadedJob, error) {
	if err := contextError(ctx); err != nil {
		return loadedJob{}, err
	}
	jobDir, err := s.existingJobDir(jobID)
	if err != nil {
		return loadedJob{}, err
	}
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

	eventLogBytes := eventsInfo.Size()
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
			_ = writeJSONAtomic(snapshotPath, wantSnapshot, maxSnapshotBytes)
		}
	}
	return loadedJob{
		spec:   spec,
		state:  replay.State,
		replay: replay,
	}, nil
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
	}
	out.Artifacts = slices.Clone(state.Artifacts)
	if state.LastDecision != nil {
		decision := *state.LastDecision
		if state.LastDecision.NextBatch != nil {
			batch := cloneBatchForStore(*state.LastDecision.NextBatch)
			decision.NextBatch = &batch
		}
		out.LastDecision = &decision
	}
	out.StagnationFingerprints = slices.Clone(state.StagnationFingerprints)
	return out
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
