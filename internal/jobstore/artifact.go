package jobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/jobs"
)

const (
	artifactMetadataVersion  = 1
	artifactDataName         = "data"
	artifactMetadataName     = "metadata.json"
	maxArtifactMetadataBytes = 64 << 10
)

type artifactStore struct {
	root      string
	maxBytes  int64
	rootError error
}

type artifactMetadata struct {
	Version    int              `json:"version"`
	JobID      string           `json:"job_id"`
	ArtifactID string           `json:"artifact_id"`
	Bytes      int64            `json:"bytes"`
	Ref        jobs.ArtifactRef `json:"ref"`
}

func newArtifactStore(root string, maxBytes int64) *artifactStore {
	root = strings.TrimSpace(root)
	if root == "" {
		return &artifactStore{maxBytes: maxBytes, rootError: fmt.Errorf("artifact root is required")}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return &artifactStore{maxBytes: maxBytes, rootError: fmt.Errorf("resolve artifact root: %w", err)}
	}
	absolute = filepath.Clean(absolute)
	if evaluated, evalErr := filepath.EvalSymlinks(absolute); evalErr == nil {
		absolute = evaluated
	}
	return &artifactStore{root: absolute, maxBytes: maxBytes}
}

func (s *artifactStore) Put(
	ctx context.Context,
	jobID, artifactID, mediaType, createdByAttemptID string,
	reader io.Reader,
) (jobs.ArtifactRef, error) {
	if err := s.validate(); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if err := contextError(ctx); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if reader == nil {
		return jobs.ArtifactRef{}, fmt.Errorf("artifact reader is required")
	}
	if err := validateArtifactID("job id", jobID); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if err := validateArtifactID("artifact id", artifactID); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if createdByAttemptID != "" {
		if err := validateReferenceID("created_by_attempt_id", createdByAttemptID); err != nil {
			return jobs.ArtifactRef{}, err
		}
	}
	mediaType = strings.TrimSpace(mediaType)
	if mediaType != "" {
		if _, _, err := mime.ParseMediaType(mediaType); err != nil {
			return jobs.ArtifactRef{}, fmt.Errorf("invalid media type %q: %w", mediaType, err)
		}
	}

	artifactsDir, err := s.ensureArtifactsDir(jobID)
	if err != nil {
		return jobs.ArtifactRef{}, err
	}
	finalDir, err := containedJoin(artifactsDir, artifactID)
	if err != nil {
		return jobs.ArtifactRef{}, err
	}
	if _, err := os.Lstat(finalDir); err == nil {
		return jobs.ArtifactRef{}, fmt.Errorf("%w: artifact %q for job %q", ErrAlreadyExists, artifactID, jobID)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return jobs.ArtifactRef{}, fmt.Errorf("inspect artifact destination: %w", err)
	}

	tempDir, err := os.MkdirTemp(artifactsDir, ".tmp-artifact-")
	if err != nil {
		return jobs.ArtifactRef{}, fmt.Errorf("create artifact temporary directory: %w", err)
	}
	defer func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return jobs.ArtifactRef{}, fmt.Errorf("secure artifact temporary directory: %w", err)
	}

	dataPath := filepath.Join(tempDir, artifactDataName)
	data, err := os.OpenFile(dataPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return jobs.ArtifactRef{}, fmt.Errorf("create artifact data: %w", err)
	}
	digest := sha256.New()
	bounded := &boundedArtifactWriter{
		writer:    io.MultiWriter(data, digest),
		remaining: s.maxBytes,
	}
	source := io.Reader(contextReader{ctx: nonNilContext(ctx), reader: reader})
	if s.maxBytes < int64(^uint64(0)>>1) {
		source = io.LimitReader(source, s.maxBytes+1)
	}
	_, copyErr := io.Copy(bounded, source)
	if copyErr == nil {
		copyErr = contextError(ctx)
	}
	if copyErr == nil {
		copyErr = data.Sync()
	}
	closeErr := data.Close()
	if copyErr != nil {
		if errors.Is(copyErr, ErrTooLarge) {
			return jobs.ArtifactRef{}, &TooLargeError{
				Resource: "artifact " + artifactID,
				Limit:    s.maxBytes,
			}
		}
		return jobs.ArtifactRef{}, fmt.Errorf("write artifact data: %w", copyErr)
	}
	if closeErr != nil {
		return jobs.ArtifactRef{}, fmt.Errorf("close artifact data: %w", closeErr)
	}

	ref := jobs.ArtifactRef{
		ID:                 artifactID,
		URI:                artifactURI(jobID, artifactID),
		SHA256:             hex.EncodeToString(digest.Sum(nil)),
		MediaType:          mediaType,
		CreatedByAttemptID: createdByAttemptID,
	}
	metadata := artifactMetadata{
		Version:    artifactMetadataVersion,
		JobID:      jobID,
		ArtifactID: artifactID,
		Bytes:      bounded.written,
		Ref:        ref,
	}
	if err := writeArtifactMetadata(filepath.Join(tempDir, artifactMetadataName), metadata); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if err := syncDirectory(tempDir); err != nil {
		return jobs.ArtifactRef{}, fmt.Errorf("sync artifact temporary directory: %w", err)
	}
	if err := contextError(ctx); err != nil {
		return jobs.ArtifactRef{}, err
	}
	if err := publishDirectoryNoReplace(tempDir, finalDir); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, ErrAlreadyExists) {
			return jobs.ArtifactRef{}, fmt.Errorf("%w: artifact %q for job %q", ErrAlreadyExists, artifactID, jobID)
		}
		return jobs.ArtifactRef{}, fmt.Errorf("publish artifact: %w", err)
	}
	tempDir = ""
	if err := syncDirectory(artifactsDir); err != nil {
		return ref, &CommitError{
			Operation: "put_artifact",
			JobID:     jobID,
			Err:       fmt.Errorf("sync artifacts directory: %w", err),
		}
	}
	return ref, nil
}

func (s *artifactStore) Open(
	ctx context.Context,
	jobID, artifactID string,
) (io.ReadCloser, jobs.ArtifactRef, error) {
	if err := s.validate(); err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	if err := contextError(ctx); err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	if err := validateArtifactID("job id", jobID); err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	if err := validateArtifactID("artifact id", artifactID); err != nil {
		return nil, jobs.ArtifactRef{}, err
	}

	artifactDir, err := s.existingArtifactDir(jobID, artifactID)
	if err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	metadata, err := readArtifactMetadata(filepath.Join(artifactDir, artifactMetadataName), jobID, artifactID)
	if err != nil {
		return nil, jobs.ArtifactRef{}, err
	}
	if metadata.Bytes > s.maxBytes {
		return nil, jobs.ArtifactRef{}, &TooLargeError{
			Resource: "artifact " + artifactID,
			Limit:    s.maxBytes,
			Actual:   metadata.Bytes,
		}
	}

	dataPath := filepath.Join(artifactDir, artifactDataName)
	data, err := openRegularRead(dataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, jobs.ArtifactRef{}, fmt.Errorf("%w: artifact %q data is missing", ErrTampered, artifactID)
		}
		return nil, jobs.ArtifactRef{}, err
	}
	opened, err := data.Stat()
	if err != nil {
		_ = data.Close()
		return nil, jobs.ArtifactRef{}, fmt.Errorf("stat opened artifact data: %w", err)
	}
	if opened.Size() != metadata.Bytes {
		_ = data.Close()
		return nil, jobs.ArtifactRef{}, fmt.Errorf(
			"%w: artifact %q size is %d, metadata declares %d",
			ErrTampered,
			artifactID,
			opened.Size(),
			metadata.Bytes,
		)
	}
	spool, err := os.CreateTemp(artifactDir, ".verified-read-")
	if err != nil {
		_ = data.Close()
		return nil, jobs.ArtifactRef{}, fmt.Errorf("create verified artifact snapshot: %w", err)
	}
	spoolPath := spool.Name()
	fail := func(err error) (io.ReadCloser, jobs.ArtifactRef, error) {
		if data != nil {
			_ = data.Close()
		}
		_ = spool.Close()
		_ = os.Remove(spoolPath)
		return nil, jobs.ArtifactRef{}, err
	}
	if err := spool.Chmod(0o600); err != nil {
		return fail(fmt.Errorf("secure verified artifact snapshot: %w", err))
	}

	digest := sha256.New()
	bounded := &boundedArtifactWriter{writer: io.MultiWriter(spool, digest), remaining: s.maxBytes}
	if _, err := io.Copy(bounded, contextReader{ctx: nonNilContext(ctx), reader: data}); err != nil {
		if errors.Is(err, ErrTooLarge) {
			return fail(&TooLargeError{
				Resource: "artifact " + artifactID,
				Limit:    s.maxBytes,
			})
		}
		return fail(fmt.Errorf("verify artifact data: %w", err))
	}
	gotSHA256 := hex.EncodeToString(digest.Sum(nil))
	if bounded.written != metadata.Bytes || gotSHA256 != metadata.Ref.SHA256 {
		return fail(fmt.Errorf(
			"%w: artifact %q sha256/size mismatch",
			ErrTampered,
			artifactID,
		))
	}
	if err := data.Close(); err != nil {
		return fail(fmt.Errorf("close verified artifact source: %w", err))
	}
	data = nil
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return fail(fmt.Errorf("rewind verified artifact: %w", err))
	}
	// On POSIX this makes the verified snapshot unreachable by pathname while
	// the returned descriptor remains readable. The cleanup wrapper covers
	// platforms/filesystems that cannot unlink an open file.
	if err := os.Remove(spoolPath); err == nil {
		spoolPath = ""
	}
	return &verifiedArtifactReader{File: spool, path: spoolPath}, metadata.Ref, nil
}

type verifiedArtifactReader struct {
	*os.File
	path string
}

func (reader *verifiedArtifactReader) Close() error {
	if reader == nil {
		return nil
	}
	var errs []error
	if reader.File != nil {
		errs = append(errs, reader.File.Close())
		reader.File = nil
	}
	if reader.path != "" {
		if err := os.Remove(reader.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		reader.path = ""
	}
	return errors.Join(errs...)
}

func (s *artifactStore) validate() error {
	if s == nil {
		return fmt.Errorf("artifact store is nil")
	}
	if s.rootError != nil {
		return s.rootError
	}
	if s.root == "" {
		return fmt.Errorf("artifact root is required")
	}
	if s.maxBytes <= 0 {
		return fmt.Errorf("artifact max bytes must be positive")
	}
	return nil
}

func (s *artifactStore) ensureArtifactsDir(jobID string) (string, error) {
	jobDir, err := containedJoin(s.root, jobID)
	if err != nil {
		return "", err
	}
	if err := requirePrivateDirectory(jobDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: job %q", ErrNotFound, jobID)
		}
		return "", err
	}
	artifactsDir := filepath.Join(jobDir, "artifacts")
	if err := os.Mkdir(artifactsDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("create artifacts directory: %w", err)
	}
	if err := requirePrivateDirectory(artifactsDir); err != nil {
		return "", err
	}
	if err := syncDirectory(jobDir); err != nil {
		return "", fmt.Errorf("sync job directory: %w", err)
	}
	return artifactsDir, nil
}

func (s *artifactStore) existingArtifactDir(jobID, artifactID string) (string, error) {
	jobDir, err := containedJoin(s.root, jobID)
	if err != nil {
		return "", err
	}
	if err := requirePrivateDirectory(jobDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: job %q", ErrNotFound, jobID)
		}
		return "", err
	}
	artifactsDir := filepath.Join(jobDir, "artifacts")
	if err := requirePrivateDirectory(artifactsDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: artifact %q for job %q", ErrNotFound, artifactID, jobID)
		}
		return "", err
	}
	artifactDir, err := containedJoin(artifactsDir, artifactID)
	if err != nil {
		return "", err
	}
	if err := requirePrivateDirectory(artifactDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: artifact %q for job %q", ErrNotFound, artifactID, jobID)
		}
		return "", err
	}
	return artifactDir, nil
}

func writeArtifactMetadata(path string, metadata artifactMetadata) error {
	body, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode artifact metadata: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxArtifactMetadataBytes {
		return fmt.Errorf("%w: artifact metadata exceeds %d bytes", ErrTooLarge, maxArtifactMetadataBytes)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact metadata: %w", err)
	}
	writeErr := writeAll(file, body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write artifact metadata: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close artifact metadata: %w", closeErr)
	}
	return nil
}

func readArtifactMetadata(path, jobID, artifactID string) (artifactMetadata, error) {
	file, err := openRegularRead(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return artifactMetadata{}, fmt.Errorf("%w: artifact %q metadata is missing", ErrCorrupt, artifactID)
		}
		return artifactMetadata{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return artifactMetadata{}, fmt.Errorf("stat artifact metadata: %w", err)
	}
	if info.Size() <= 0 || info.Size() > maxArtifactMetadataBytes {
		return artifactMetadata{}, fmt.Errorf("%w: invalid artifact metadata size %d", ErrCorrupt, info.Size())
	}
	body, err := io.ReadAll(io.LimitReader(file, maxArtifactMetadataBytes+1))
	if err != nil {
		return artifactMetadata{}, fmt.Errorf("read artifact metadata: %w", err)
	}
	var metadata artifactMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return artifactMetadata{}, fmt.Errorf("%w: decode artifact metadata: %v", ErrCorrupt, err)
	}
	canonical, err := json.Marshal(metadata)
	if err != nil {
		return artifactMetadata{}, fmt.Errorf("%w: canonicalize artifact metadata: %v", ErrCorrupt, err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(body, canonical) {
		return artifactMetadata{}, fmt.Errorf("%w: artifact metadata is not canonical", ErrCorrupt)
	}
	if metadata.Version != artifactMetadataVersion {
		return artifactMetadata{}, fmt.Errorf("%w: unsupported artifact metadata version %d", ErrCorrupt, metadata.Version)
	}
	if metadata.JobID != jobID || metadata.ArtifactID != artifactID {
		return artifactMetadata{}, fmt.Errorf("%w: artifact metadata identity mismatch", ErrCorrupt)
	}
	if metadata.Bytes < 0 {
		return artifactMetadata{}, fmt.Errorf("%w: negative artifact byte count", ErrCorrupt)
	}
	if err := metadata.Ref.Validate(); err != nil {
		return artifactMetadata{}, fmt.Errorf("%w: invalid artifact reference: %v", ErrCorrupt, err)
	}
	if metadata.Ref.ID != artifactID || metadata.Ref.URI != artifactURI(jobID, artifactID) {
		return artifactMetadata{}, fmt.Errorf("%w: artifact reference identity mismatch", ErrCorrupt)
	}
	if metadata.Ref.MediaType != "" {
		if _, _, err := mime.ParseMediaType(metadata.Ref.MediaType); err != nil {
			return artifactMetadata{}, fmt.Errorf("%w: invalid artifact media type: %v", ErrCorrupt, err)
		}
	}
	if err := validateSHA256(metadata.Ref.SHA256); err != nil {
		return artifactMetadata{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return metadata, nil
}

func regularFileInfo(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: refusing non-regular file %s", ErrCorrupt, path)
	}
	if err := validateRegularFilePlatform(info, path); err != nil {
		return nil, err
	}
	return info, nil
}

func requirePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: refusing non-directory path %s", ErrCorrupt, path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure directory %s: %w", path, err)
	}
	return nil
}

func containedJoin(root string, elements ...string) (string, error) {
	root = filepath.Clean(root)
	targetElements := append([]string{root}, elements...)
	target := filepath.Join(targetElements...)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path escapes store root", ErrInvalidID)
	}
	return target, nil
}

func validateArtifactID(label, id string) error {
	if err := ValidatePortableID(id); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// validateReferenceID matches the wider in-memory domain grammar. References
// are metadata only and are never joined into filesystem paths.
func validateReferenceID(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 128 {
		return fmt.Errorf("%s: %w", label, &InvalidIDError{Value: value})
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(index > 0 && r >= '0' && r <= '9') ||
			(index > 0 && (r == '-' || r == '_' || r == '.' || r == ':')) {
			continue
		}
		return fmt.Errorf("%s: %w", label, &InvalidIDError{Value: value})
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return fmt.Errorf("invalid sha256 %q", value)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid sha256 %q", value)
	}
	return nil
}

func artifactURI(jobID, artifactID string) string {
	return "job://" + jobID + "/artifacts/" + artifactID
}

func writeAll(writer io.Writer, body []byte) error {
	for len(body) > 0 {
		written, err := writer.Write(body)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		body = body[written:]
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}

type boundedArtifactWriter struct {
	writer    io.Writer
	remaining int64
	written   int64
}

func (w *boundedArtifactWriter) Write(body []byte) (int, error) {
	if int64(len(body)) > w.remaining {
		allowed := int(w.remaining)
		if allowed > 0 {
			written, err := w.writer.Write(body[:allowed])
			w.written += int64(written)
			w.remaining -= int64(written)
			if err != nil {
				return written, err
			}
			if written != allowed {
				return written, io.ErrShortWrite
			}
		}
		return allowed, ErrTooLarge
	}
	written, err := w.writer.Write(body)
	w.written += int64(written)
	w.remaining -= int64(written)
	return written, err
}
