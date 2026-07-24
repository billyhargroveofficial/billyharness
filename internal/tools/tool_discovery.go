package tools

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools/discovery"
)

func (r *Registry) addToolSearch() {
	r.add(Tool{
		Spec: protocol.ToolSpec{
			Name:        "tool_search",
			Description: "Search static model-visible gateway tools and the dynamic MCP catalog by name or description. MCP results are call hints for mcp_call, not direct model-visible tool specs.",
			Parameters:  raw(`{"type":"object","properties":{"query":{"type":"string","description":"Tool capability, name, or description text to search for. Empty returns the first matching tools."},"server":{"type":"string","description":"Optional MCP server filter: telegram, telegram-parilka, github, or context7."},"namespace":{"type":"string","description":"Optional namespace filter such as fs, web, shell, mcp, mcp.github, or telegram-parilka."},"risk":{"type":"string","description":"Optional risk filter: read_only, network, write, execute, or external."},"limit":{"type":"integer","default":20},"include_schema":{"type":"boolean","default":false,"description":"Include input schemas for matching tools when exact arguments are needed, capped by max_schema_tokens."},"max_schema_tokens":{"type":"integer","default":1200,"description":"Maximum estimated schema tokens to include across all returned tools."}},"additionalProperties":false}`),
			Risk:        protocol.RiskReadOnly,
		},
		Handler: func(ctx context.Context, args json.RawMessage) (Result, error) {
			var in struct {
				Query           string `json:"query"`
				Server          string `json:"server"`
				Namespace       string `json:"namespace"`
				Risk            string `json:"risk"`
				Limit           int    `json:"limit"`
				IncludeSchema   bool   `json:"include_schema"`
				MaxSchemaTokens int    `json:"max_schema_tokens"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return Result{}, err
			}
			if in.Limit <= 0 || in.Limit > 80 {
				in.Limit = 20
			}
			r.refreshMCPTools(ctx)
			capabilities := r.runCapabilitiesForContext(ctx)
			results := r.searchToolsWithCapabilities(in.Query, in.Server, in.Namespace, in.Risk, in.Limit, in.IncludeSchema, in.MaxSchemaTokens, capabilities)
			catalog := r.mcpCatalogSnapshot()
			modelVisible := r.modelVisibleToolCatalogSnapshotWithCapabilities(capabilities)
			out, _ := json.MarshalIndent(map[string]any{
				"tools":               results.Items,
				"truncated":           results.Truncated,
				"metrics":             results.Metrics,
				"model_visible_tools": modelVisible,
				"mcp_catalog":         catalog,
			}, "", "  ")
			metadata := addMCPCatalogMetadata(results.Metrics.Metadata(), catalog)
			metadata = addModelVisibleToolMetadata(metadata, modelVisible)
			return Result{Content: string(out), Metadata: metadata}, nil
		},
	})
}

func (r *Registry) searchTools(query, server, namespace, risk string, limit int, includeSchema bool, maxSchemaTokens int) discovery.Results {
	return r.searchToolsWithCapabilities(query, server, namespace, risk, limit, includeSchema, maxSchemaTokens, r.runCapabilities)
}

func (r *Registry) searchToolsWithCapabilities(query, server, namespace, risk string, limit int, includeSchema bool, maxSchemaTokens int, capabilities RunCapabilities) discovery.Results {
	if limit <= 0 {
		limit = 20
	}
	return discovery.Search(r.discoveryCandidatesWithCapabilities(true, true, capabilities), discovery.Query{
		Query:           query,
		Server:          server,
		Namespace:       namespace,
		Risk:            risk,
		Limit:           limit,
		IncludeSchema:   includeSchema,
		MaxSchemaTokens: maxSchemaTokens,
	})
}

func (r *Registry) discoveryCandidates(includeNative, includeMCP bool) []discovery.Candidate {
	return r.discoveryCandidatesWithCapabilities(includeNative, includeMCP, r.runCapabilities)
}

func (r *Registry) discoveryCandidatesWithCapabilities(includeNative, includeMCP bool, capabilities RunCapabilities) []discovery.Candidate {
	var candidates []discovery.Candidate
	nativeNames := make([]string, 0, len(r.tools))
	if includeNative {
		for name := range r.tools {
			nativeNames = append(nativeNames, name)
		}
		sort.Strings(nativeNames)
		for _, name := range nativeNames {
			if !capabilities.AllowsTool(name) {
				continue
			}
			tool := r.tools[name]
			candidates = append(candidates, discovery.Candidate{
				Spec:      tool.Spec,
				Source:    discovery.SourceNative,
				Namespace: discovery.NativeNamespace(tool.Spec.Name),
				CallTool:  tool.Spec.Name,
			})
		}
	}

	mcpTools := r.mcpToolsSnapshot()
	mcpNames := make([]string, 0, len(mcpTools))
	if includeMCP {
		if !capabilities.AllowsTool("mcp_call") {
			return candidates
		}
		for name := range mcpTools {
			mcpNames = append(mcpNames, name)
		}
		sort.Strings(mcpNames)
		for _, name := range mcpNames {
			tool := mcpTools[name]
			serverName := discovery.MCPServerFromToolName(name)
			riskSource := tool.mcpPolicy.RiskSource
			if riskSource == "" {
				riskSource = "unclassified_mcp_catalog"
			}
			metadataTrust := tool.mcpPolicy.MetadataTrust
			if metadataTrust == "" {
				metadataTrust = mcpclient.MCPMetadataTrustUntrusted
			}
			schemaReport := externalMCPSchemaReport(tool.Spec.Parameters)
			candidates = append(candidates, discovery.Candidate{
				Spec:                           tool.Spec,
				Source:                         discovery.SourceMCP,
				Namespace:                      discovery.MCPNamespace(serverName),
				Server:                         serverName,
				CallTool:                       "mcp_call",
				CallName:                       tool.Spec.Name,
				RiskSource:                     riskSource,
				MetadataTrust:                  metadataTrust,
				InputSchemaValidation:          schemaReport.Mode,
				InputSchemaUnsupportedKeywords: schemaReport.UnsupportedKeywords,
			})
		}
	}
	return candidates
}
