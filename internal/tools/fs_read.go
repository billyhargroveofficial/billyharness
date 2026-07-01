package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	defaultFSReadLimit    = 200
	maxFSReadLimit        = 1000
	maxFSReadLineRunes    = 2000
	fsReadTruncationLabel = "...[line truncated"
)

type fsReadInput struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

func (r *Registry) addFSRead() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_read_file",
			Description: "Read a UTF-8 file from the allowed workspace. With offset/limit, return a bounded 1-indexed line window with line numbers and truncation metadata.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","description":"Optional 1-indexed starting line for a bounded window."},"limit":{"type":"integer","default":200,"description":"Optional number of lines to return for a bounded window; clamped to 1000."}},"required":["path"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: r.handleFSRead,
	})
}

func (r *Registry) handleFSRead(_ context.Context, args json.RawMessage) (Result, error) {
	var in fsReadInput
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	path, err := r.safePath(in.Path)
	if err != nil {
		return Result{}, err
	}
	if in.Offset == nil && in.Limit == nil {
		bytes, err := os.ReadFile(path)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: string(bytes)}, nil
	}
	return readFSLineWindow(path, in)
}

func readFSLineWindow(path string, in fsReadInput) (Result, error) {
	offset := 1
	if in.Offset != nil && *in.Offset > 0 {
		offset = *in.Offset
	}
	limit := defaultFSReadLimit
	if in.Limit != nil && *in.Limit > 0 {
		limit = *in.Limit
	}
	if limit > maxFSReadLimit {
		limit = maxFSReadLimit
	}

	file, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var lines []string
	lineNo := 0
	lineCount := 0
	lineEnd := 0
	longLinesTruncated := 0
	for {
		rawLine, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return Result{}, readErr
		}
		if rawLine != "" {
			lineNo++
			line := trimFSReadLineBreak(rawLine)
			if strings.ContainsRune(line, '\x00') || !utf8.ValidString(line) {
				return Result{}, fmt.Errorf("refusing binary or non-UTF-8 file %s", path)
			}
			if lineNo >= offset && lineCount < limit {
				display, truncated := truncateFSReadLine(line)
				if truncated {
					longLinesTruncated++
				}
				lines = append(lines, fmt.Sprintf("%d: %s", lineNo, display))
				lineEnd = lineNo
				lineCount++
			}
		}
		if readErr == io.EOF {
			break
		}
	}

	nextOffset := 0
	if lineCount > 0 && lineEnd < lineNo {
		nextOffset = lineEnd + 1
		lines = append(lines, fmt.Sprintf("...[truncated; next_offset=%d total_lines=%d]", nextOffset, lineNo))
	}
	if lineCount == 0 && offset > lineNo {
		lines = append(lines, fmt.Sprintf("...[no lines at offset %d; total_lines=%d]", offset, lineNo))
	}
	truncated := nextOffset > 0 || longLinesTruncated > 0
	lineStart := 0
	if lineCount > 0 {
		lineStart = offset
	}
	metadata := map[string]any{
		"path":                 path,
		"offset":               offset,
		"limit":                limit,
		"line_start":           lineStart,
		"line_end":             lineEnd,
		"line_count":           lineCount,
		"total_lines":          lineNo,
		"next_offset":          nextOffset,
		"truncated":            truncated,
		"lines_truncated":      nextOffset > 0,
		"long_lines_truncated": longLinesTruncated,
	}
	return Result{
		Content:   strings.Join(lines, "\n"),
		Metadata:  metadata,
		Truncated: truncated,
	}, nil
}

func trimFSReadLineBreak(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

func truncateFSReadLine(line string) (string, bool) {
	runes := []rune(line)
	if len(runes) <= maxFSReadLineRunes {
		return line, false
	}
	omitted := len(runes) - maxFSReadLineRunes
	return string(runes[:maxFSReadLineRunes]) + fmt.Sprintf("%s %d chars]", fsReadTruncationLabel, omitted), true
}
