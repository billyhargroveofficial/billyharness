package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/filesearch"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type fsFindFilesInput struct {
	Query  string `json:"query"`
	Path   string `json:"path"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

func (r *Registry) addFSFindFiles() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "fs_find_files",
			Description: "Find workspace files by fuzzy basename or path. Returns ranked relative paths with type, score, source, offset, limit, sensitive-path skips, and deterministic truncation.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string","description":"Partial basename or path to rank. Empty returns the first bounded paths."},"path":{"type":"string","default":".","description":"Workspace directory or file subtree to search."},"limit":{"type":"integer","default":40,"description":"Maximum returned files; clamped to 500."},"offset":{"type":"integer","default":0,"description":"Number of ranked matches to skip before returning results."}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: r.handleFSFindFiles,
	})
}

func (r *Registry) handleFSFindFiles(ctx context.Context, args json.RawMessage) (Result, error) {
	var in fsFindFilesInput
	if err := json.Unmarshal(args, &in); err != nil {
		return Result{}, err
	}
	in.Path = defaultString(in.Path, ".")
	in.Query = strings.TrimSpace(in.Query)
	in.Limit = clampPositive(in.Limit, filesearch.DefaultLimit, filesearch.MaxLimit)
	if in.Offset < 0 {
		in.Offset = 0
	}
	if _, err := r.safePath(in.Path); err != nil {
		return Result{}, err
	}
	resolver := r.fileResolver
	if resolver == nil {
		resolver = filesearch.NewResolver(filesearch.DefaultCacheTTL)
	}
	result, err := resolver.Find(ctx, filesearch.Options{
		Roots:  r.toolPolicy.WorkspaceRoots,
		Path:   in.Path,
		Query:  in.Query,
		Limit:  in.Limit,
		Offset: in.Offset,
	})
	if err != nil {
		return Result{}, err
	}
	lines := make([]string, 0, len(result.Matches)+1)
	for _, match := range result.Matches {
		lines = append(lines, fmt.Sprintf("%s\t%s\tscore=%d\tsource=%s", match.Path, match.Type, match.Score, match.Source))
	}
	if len(lines) == 0 {
		lines = append(lines, "no matches")
	}
	if result.Truncated {
		lines = append(lines, fmt.Sprintf("...[truncated; next_offset=%d]", result.NextOffset))
	}
	metadata := map[string]any{
		"tool":                    "fs_find_files",
		"path":                    in.Path,
		"query":                   in.Query,
		"limit":                   in.Limit,
		"offset":                  in.Offset,
		"next_offset":             result.NextOffset,
		"matches":                 result.Total,
		"returned_matches":        result.Returned,
		"candidates":              result.Candidates,
		"source":                  result.Source,
		"files_skipped_sensitive": result.FilesSkippedSensitive,
		"files_skipped_outside":   result.FilesSkippedOutsideRoot,
		"truncated":               result.Truncated,
		"candidates_truncated":    result.CandidatesTruncated,
		"display_summary":         fsFindFilesSummary(result.Returned, result.Total, result.Truncated),
		"display_target":          in.Query,
	}
	return Result{Content: strings.Join(lines, "\n"), Metadata: metadata, Truncated: result.Truncated}, nil
}

func fsFindFilesSummary(returned, total int, truncated bool) string {
	summary := fmt.Sprintf("%d/%d file matches", returned, total)
	if total == 1 {
		summary = fmt.Sprintf("%d/%d file match", returned, total)
	}
	if truncated {
		summary += " (truncated)"
	}
	return summary
}
