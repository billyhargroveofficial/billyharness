package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/memory"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func (r *Registry) addMemory() {
	if r == nil || !r.toolPolicy.MemoryEnabled {
		return
	}
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "memory_list",
			Description: "List Billyharness memory index entries from the local home/profile memory roots. Returns summaries and paths only, not topic bodies.",
			Parameters:  raw(`{"type":"object","properties":{"source":{"type":"string","description":"Optional source filter: home or profile."},"query":{"type":"string","description":"Optional case-insensitive filter over source/type/topic/summary/path."},"limit":{"type":"integer","default":50}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyReadOnly, Idempotent: true, Cancellable: true},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in memory.OperationInput
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			in.Op = "list"
			return r.memoryResult(in)
		},
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "memory_search",
			Description: "Search Billyharness memory summaries and bounded topic bodies by query. Use memory_read for exact topic content.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string"},"source":{"type":"string","description":"Optional source filter: home or profile."},"limit":{"type":"integer","default":20},"max_bytes":{"type":"integer","default":12288,"description":"Maximum bytes to read from each topic file while searching."}},"required":["query"],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyReadOnly, Idempotent: true, Cancellable: true},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in memory.OperationInput
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			in.Op = "search"
			return r.memoryResult(in)
		},
	})
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "memory_read",
			Description: "Read one Billyharness memory topic by topic or path with a bounded byte cap.",
			Parameters:  raw(`{"type":"object","properties":{"source":{"type":"string","description":"Optional source filter: home or profile."},"topic":{"type":"string"},"path":{"type":"string"},"max_bytes":{"type":"integer","default":12288}},"anyOf":[{"required":["topic"]},{"required":["path"]}],"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyReadOnly, Idempotent: true, Cancellable: true},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in memory.OperationInput
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			in.Op = "read"
			return r.memoryResult(in)
		},
	})
	r.add(memoryWriteTool("memory_add", "Preview or add a Billyharness memory topic. Mutates only when confirm=true; body defaults to summary when omitted.", func(_ context.Context, args json.RawMessage) (Result, error) {
		var in memory.OperationInput
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{}, err
		}
		in.Op = "add"
		return r.memoryResult(in)
	}))
	r.add(memoryWriteTool("memory_replace", "Preview or replace an existing Billyharness memory topic. Mutates only when confirm=true.", func(_ context.Context, args json.RawMessage) (Result, error) {
		var in memory.OperationInput
		if err := json.Unmarshal(args, &in); err != nil {
			return Result{}, err
		}
		in.Op = "replace"
		return r.memoryResult(in)
	}))
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "memory_remove",
			Description: "Preview or remove one Billyharness memory index entry and topic file. Mutates only when confirm=true.",
			Parameters:  raw(`{"type":"object","properties":{"source":{"type":"string","description":"Memory source: home or profile."},"topic":{"type":"string"},"path":{"type":"string"},"confirm":{"type":"boolean","default":false,"description":"Required true to remove the index entry and topic file."}},"anyOf":[{"required":["topic"]},{"required":["path"]}],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyExclusiveWorkspace, RequiresExclusiveWorkspace: true, Cancellable: true, MaxConcurrency: 1},
		Handler: func(_ context.Context, args json.RawMessage) (Result, error) {
			var in memory.OperationInput
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			in.Op = "remove"
			return r.memoryResult(in)
		},
	})
}

func memoryWriteTool(name, description string, handler func(context.Context, json.RawMessage) (Result, error)) Tool {
	return Tool{
		Spec: protocol.ToolSpec{
			Name:        name,
			Description: description,
			Parameters:  raw(`{"type":"object","properties":{"source":{"type":"string","description":"Memory source: home or profile."},"type":{"type":"string","description":"Memory type such as user, feedback, project, or reference."},"topic":{"type":"string"},"summary":{"type":"string","description":"Short prompt-safe summary for the memory index."},"path":{"type":"string","description":"Relative topic markdown path under the memory root."},"body":{"type":"string","description":"Full topic body to write; defaults to summary when omitted."},"confirm":{"type":"boolean","default":false,"description":"Required true to mutate memory files."}},"required":["type","topic","summary","path"],"additionalProperties":false}`),
			Risk:        protocol.RiskWrite,
		},
		Parallel: ParallelMetadata{Policy: ParallelPolicyExclusiveWorkspace, RequiresExclusiveWorkspace: true, Cancellable: true, MaxConcurrency: 1},
		Handler:  handler,
	}
}

func (r *Registry) memoryResult(in memory.OperationInput) (Result, error) {
	out, err := memory.Execute(r.memorySettings(), in)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Content:  out,
		Metadata: memoryMetadata(in, out),
	}, nil
}

func (r *Registry) memorySettings() config.InstructionSettings {
	if r == nil {
		return config.InstructionSettings{MemoryEnabled: true}
	}
	return config.InstructionSettings{
		Profile:               r.profile,
		WorkspaceRoots:        append([]string(nil), r.toolPolicy.WorkspaceRoots...),
		ProjectDocMaxBytes:    r.toolPolicy.ProjectDocMaxBytes,
		ProjectDocFallbacks:   append([]string(nil), r.toolPolicy.ProjectDocFallbacks...),
		MemoryEnabled:         true,
		MemorySummaryMaxBytes: r.toolPolicy.MemorySummaryMaxBytes,
		MemoryIndexMaxBytes:   r.toolPolicy.MemoryIndexMaxBytes,
		MemoryTopicMaxBytes:   r.toolPolicy.MemoryTopicMaxBytes,
	}
}

func memoryMetadata(in memory.OperationInput, out string) map[string]any {
	op := strings.TrimSpace(in.Op)
	if op == "" {
		op = "list"
	}
	preview := out
	if i := strings.IndexByte(preview, '\n'); i >= 0 {
		preview = preview[:i]
	}
	return map[string]any{
		"display_group":    "memory",
		"display_summary":  "memory " + op,
		"display_target":   firstMemoryTarget(in),
		"display_preview":  preview,
		"collapse_default": true,
		"memory_op":        op,
		"memory_source":    in.Source,
		"memory_topic":     in.Topic,
		"memory_path":      in.Path,
		"memory_confirm":   in.Confirm,
	}
}

func firstMemoryTarget(in memory.OperationInput) string {
	for _, value := range []string{in.Topic, in.Path, in.Query, in.Source} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "memory"
}
