package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const maxFSEditEdits = 20

type fsEditInput struct {
	Path           string       `json:"path"`
	ExpectedSHA256 string       `json:"expected_sha256,omitempty"`
	Edits          []fsEditSpec `json:"edits"`
}

type fsEditSpec struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (r *Registry) addFSEdit() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_edit_file",
			Description: "Edit an existing UTF-8 workspace file using exact string replacements. All edits are verified before a single atomic write; no fuzzy matching.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string","description":"Optional lowercase hex sha256 of the current file content."},"edits":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean","default":false}},"required":["old_string","new_string"],"additionalProperties":false}}},"required":["path","edits"],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Handler: r.handleFSEdit,
	})
}

func (r *Registry) handleFSEdit(_ context.Context, args json.RawMessage) (Result, error) {
	var in fsEditInput
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	if len(in.Edits) == 0 {
		return Result{}, fmt.Errorf("edits required")
	}
	if len(in.Edits) > maxFSEditEdits {
		return Result{}, fmt.Errorf("too many edits: %d > %d", len(in.Edits), maxFSEditEdits)
	}
	path, err := r.safePath(in.Path)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("refusing to edit non-regular file %s", path)
	}
	if info.Size() > maxWriteBytes {
		return Result{}, fmt.Errorf("file too large to edit: %d bytes", info.Size())
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	if strings.ContainsRune(string(beforeBytes), '\x00') || !utf8.Valid(beforeBytes) {
		return Result{}, fmt.Errorf("refusing binary or non-UTF-8 file %s", path)
	}
	beforeHash := sha256Hex(beforeBytes)
	if strings.TrimSpace(in.ExpectedSHA256) != "" {
		expected := strings.ToLower(strings.TrimSpace(in.ExpectedSHA256))
		if _, err := hex.DecodeString(expected); err != nil || len(expected) != sha256.Size*2 {
			return Result{}, fmt.Errorf("expected_sha256 must be a 64-character hex string")
		}
		if expected != beforeHash {
			return Result{}, fmt.Errorf("expected_sha256 mismatch for %s", path)
		}
	}

	text := string(beforeBytes)
	replacements := 0
	for i, edit := range in.Edits {
		next, count, err := applyExactEdit(text, edit, i)
		if err != nil {
			return Result{}, err
		}
		text = next
		replacements += count
	}
	afterBytes := []byte(text)
	if len(afterBytes) > maxWriteBytes {
		return Result{}, fmt.Errorf("edited content too large: %d bytes", len(afterBytes))
	}
	afterHash := sha256Hex(afterBytes)
	if afterHash == beforeHash {
		return Result{}, fmt.Errorf("edits produced no file change")
	}
	if err := atomicWriteFile(path, afterBytes, info.Mode().Perm()); err != nil {
		return Result{}, err
	}
	metadata := map[string]any{
		"path":            path,
		"edit_count":      len(in.Edits),
		"replacements":    replacements,
		"before_sha256":   beforeHash,
		"after_sha256":    afterHash,
		"before_bytes":    len(beforeBytes),
		"after_bytes":     len(afterBytes),
		"display_summary": fmt.Sprintf("edited %s: %d replacement%s", filepath.Base(path), replacements, pluralSuffix(replacements)),
	}
	return Result{
		Content:  fmt.Sprintf("edited %s: %d edit%s, %d replacement%s", path, len(in.Edits), pluralSuffix(len(in.Edits)), replacements, pluralSuffix(replacements)),
		Metadata: metadata,
	}, nil
}

func applyExactEdit(text string, edit fsEditSpec, index int) (string, int, error) {
	if edit.OldString == "" {
		return "", 0, fmt.Errorf("edit %d old_string must be non-empty", index+1)
	}
	if edit.OldString == edit.NewString {
		return "", 0, fmt.Errorf("edit %d old_string and new_string must differ", index+1)
	}
	if strings.ContainsRune(edit.OldString, '\x00') || strings.ContainsRune(edit.NewString, '\x00') || !utf8.ValidString(edit.OldString) || !utf8.ValidString(edit.NewString) {
		return "", 0, fmt.Errorf("edit %d contains binary or non-UTF-8 text", index+1)
	}
	count := strings.Count(text, edit.OldString)
	if count == 0 {
		return "", 0, fmt.Errorf("edit %d old_string not found", index+1)
	}
	if !edit.ReplaceAll && count > 1 {
		return "", 0, fmt.Errorf("edit %d old_string matched %d times; set replace_all=true to replace all", index+1, count)
	}
	if edit.ReplaceAll {
		return strings.ReplaceAll(text, edit.OldString, edit.NewString), count, nil
	}
	return strings.Replace(text, edit.OldString, edit.NewString, 1), 1, nil
}

func atomicWriteFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
