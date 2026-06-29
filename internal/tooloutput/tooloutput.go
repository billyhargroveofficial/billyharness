package tooloutput

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

type StoreRequest struct {
	Parts                 []string
	Content               string
	TrimSpace             bool
	EnsureTrailingNewline bool
}

type Ref struct {
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

func Store(req StoreRequest) (Ref, error) {
	content := req.Content
	if req.TrimSpace {
		content = strings.TrimSpace(content)
	}
	if content == "" {
		return Ref{}, nil
	}
	if req.EnsureTrailingNewline && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	body := []byte(content)
	now := time.Now().UTC()
	root := filepath.Join(config.BillyHomeDir(), "tool-output")
	if err := ensurePrivateDir(root); err != nil {
		return Ref{}, err
	}
	dir := filepath.Join(root, now.Format("20060102"))
	if err := ensurePrivateDir(dir); err != nil {
		return Ref{}, err
	}
	sum := sha256.Sum256(body)
	name := fileName(now, req.Parts, sum)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return Ref{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Ref{}, err
	}
	return Ref{
		Path:        path,
		ID:          filepath.Base(path),
		Bytes:       int64(len(body)),
		SHA256:      hex.EncodeToString(sum[:]),
		Permissions: "0600",
		Plaintext:   true,
	}, nil
}

func Stat(path string) (Ref, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Ref{}, fmt.Errorf("output ref path required")
	}
	file, err := os.Open(path)
	if err != nil {
		return Ref{}, err
	}
	defer file.Close()
	hash := sha256.New()
	bytes, err := io.Copy(hash, file)
	if err != nil {
		return Ref{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return Ref{}, err
	}
	return Ref{
		Path:        path,
		ID:          filepath.Base(path),
		Bytes:       bytes,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		Permissions: fmt.Sprintf("%04o", info.Mode().Perm()),
		Plaintext:   true,
	}, nil
}

func Exists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func StatMetadata(path string) (ArtifactMetadata, error) {
	ref, err := Stat(path)
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

func (r Ref) ArtifactMetadata() ArtifactMetadata {
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

func (r Ref) Metadata() map[string]any {
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

func (r Ref) AddMetadata(metadata map[string]any) {
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
