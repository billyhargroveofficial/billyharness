package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

type OutputStoreRequest struct {
	Parts                 []string
	Content               string
	TrimSpace             bool
	EnsureTrailingNewline bool
}

type OutputRef struct {
	Path        string
	ID          string
	Bytes       int64
	SHA256      string
	Permissions string
	Plaintext   bool
}

const (
	MetadataOutputRef            = "output_ref"
	MetadataOutputRefID          = "output_ref_id"
	MetadataOutputRefBytes       = "output_ref_bytes"
	MetadataOutputRefSHA256      = "output_ref_sha256"
	MetadataOutputRefPermissions = "output_ref_permissions"
	MetadataOutputRefPlaintext   = "output_ref_plaintext"
	MetadataOutputRefHashError   = "output_ref_hash_error"
)

type ArtifactMetadata struct {
	OutputRef            string `json:"output_ref,omitempty"`
	OutputRefID          string `json:"output_ref_id,omitempty"`
	OutputRefBytes       int64  `json:"output_ref_bytes,omitempty"`
	OutputRefSHA256      string `json:"output_ref_sha256,omitempty"`
	OutputRefPermissions string `json:"output_ref_permissions,omitempty"`
	OutputRefPlaintext   bool   `json:"output_ref_plaintext,omitempty"`
}

func StoreOutput(req OutputStoreRequest) (OutputRef, error) {
	content := req.Content
	if req.TrimSpace {
		content = strings.TrimSpace(content)
	}
	if content == "" {
		return OutputRef{}, nil
	}
	if req.EnsureTrailingNewline && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	body := []byte(content)
	now := time.Now().UTC()
	root := outputRoot()
	if err := ensurePrivateDir(root); err != nil {
		return OutputRef{}, err
	}
	dir := filepath.Join(root, now.Format("20060102"))
	if err := ensurePrivateDir(dir); err != nil {
		return OutputRef{}, err
	}
	sum := sha256.Sum256(body)
	name := fileName(now, req.Parts, sum)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return OutputRef{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return OutputRef{}, err
	}
	return OutputRef{
		Path:        path,
		ID:          portableID(root, path),
		Bytes:       int64(len(body)),
		SHA256:      hex.EncodeToString(sum[:]),
		Permissions: "0600",
		Plaintext:   true,
	}, nil
}

func StatOutputRef(path string) (OutputRef, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return OutputRef{}, fmt.Errorf("output ref path required")
	}
	file, err := os.Open(path)
	if err != nil {
		return OutputRef{}, err
	}
	defer file.Close()
	hash := sha256.New()
	bytes, err := io.Copy(hash, file)
	if err != nil {
		return OutputRef{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return OutputRef{}, err
	}
	return OutputRef{
		Path:        path,
		ID:          portableID(outputRoot(), path),
		Bytes:       bytes,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		Permissions: outputRefPermissionLabel(info.Mode().Perm()),
		Plaintext:   true,
	}, nil
}

func IsPortableID(id string) bool {
	id = strings.TrimSpace(filepath.ToSlash(id))
	if id == "" || id == "." || filepath.IsAbs(id) || path.IsAbs(id) {
		return false
	}
	clean := pathClean(id)
	return clean == id && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func outputRefPermissionLabel(perm os.FileMode) string {
	if runtime.GOOS == "windows" {
		return "0600"
	}
	return fmt.Sprintf("%04o", perm)
}

func outputRoot() string {
	return filepath.Join(config.BillyHomeDir(), "tool-output")
}

func portableID(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && IsPortableID(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(path)
}

func pathClean(value string) string {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." {
		return strings.TrimSpace(filepath.ToSlash(value))
	}
	return clean
}

func Exists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func StatMetadata(path string) (ArtifactMetadata, error) {
	ref, err := StatOutputRef(path)
	if err != nil {
		return ArtifactMetadata{}, err
	}
	return ref.ArtifactMetadata(), nil
}

func AddMetadataForPath(metadata map[string]any, path string) error {
	if metadata == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := StatMetadata(path)
	if err != nil {
		metadata[MetadataOutputRefHashError] = err.Error()
		return err
	}
	info.AddTo(metadata)
	return nil
}

func (r OutputRef) ArtifactMetadata() ArtifactMetadata {
	if r.Path == "" {
		return ArtifactMetadata{}
	}
	return ArtifactMetadata{
		OutputRef:            r.Path,
		OutputRefID:          r.ID,
		OutputRefBytes:       r.Bytes,
		OutputRefSHA256:      r.SHA256,
		OutputRefPermissions: r.Permissions,
		OutputRefPlaintext:   r.Plaintext,
	}
}

func (r OutputRef) Metadata() map[string]any {
	return r.ArtifactMetadata().Map()
}

func (m ArtifactMetadata) Map() map[string]any {
	if strings.TrimSpace(m.OutputRef) == "" {
		return nil
	}
	return map[string]any{
		MetadataOutputRef:            m.OutputRef,
		MetadataOutputRefID:          m.OutputRefID,
		MetadataOutputRefBytes:       m.OutputRefBytes,
		MetadataOutputRefSHA256:      m.OutputRefSHA256,
		MetadataOutputRefPermissions: m.OutputRefPermissions,
		MetadataOutputRefPlaintext:   m.OutputRefPlaintext,
	}
}

func (m ArtifactMetadata) AddTo(metadata map[string]any) {
	if metadata == nil {
		return
	}
	for key, value := range m.Map() {
		metadata[key] = value
	}
}

func (r OutputRef) AddMetadata(metadata map[string]any) {
	r.ArtifactMetadata().AddTo(metadata)
}

func fileName(now time.Time, parts []string, sum [32]byte) string {
	clean := make([]string, 0, len(parts)+2)
	clean = append(clean, now.UTC().Format("150405.000000000"))
	for _, part := range parts {
		if safe := safeName(part); safe != "" {
			clean = append(clean, safe)
		}
	}
	if len(clean) == 1 {
		clean = append(clean, "output")
	}
	clean = append(clean, hex.EncodeToString(sum[:4]))
	return strings.Join(clean, "-") + ".txt"
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

var unsafeNameRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeName(value string) string {
	value = strings.TrimSpace(value)
	if u, err := url.Parse(value); err == nil && u.Hostname() != "" {
		value = u.Hostname() + u.EscapedPath()
	}
	value = unsafeNameRE.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return ""
	}
	if len(value) > 72 {
		value = value[:72]
	}
	return value
}
