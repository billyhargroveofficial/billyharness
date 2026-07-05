package docsgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type toolsReferenceData struct {
	Risks []protocol.RiskClassSpec
	Tools []toolDoc
	MCP   []mcpFieldDoc
}

type toolDoc struct {
	Name        string
	Description string
	Risk        string
	RiskClass   string
	Decision    string
	Parallel    tools.ParallelMetadata
	Schema      json.RawMessage
}

type mcpFieldDoc struct {
	Field       string
	Description string
}

func GenerateTools() ([]byte, error) {
	data := toolsReferenceInput()
	var b bytes.Buffer
	b.Write(header("internal/tools"))
	b.WriteString("# Tool Catalog\n\n")
	b.WriteString("This reference documents the static native tool registry and MCP policy shape. Live MCP server inventories are runtime-only; inspect them with tool search or MCP status tools.\n\n")
	b.WriteString("## Risk Vocabulary\n\n")
	b.WriteString(markdownTable([]string{"Risk", "Description"}, riskRows(data.Risks)))
	b.WriteString("\n## Native Tools\n\n")
	for _, tool := range data.Tools {
		b.WriteString("### " + tool.Name + "\n\n")
		b.WriteString(tool.Description + "\n\n")
		rows := [][]string{
			{"Risk", tool.Risk},
			{"Risk class", tool.RiskClass},
			{"Policy decision", tool.Decision},
			{"Parallel policy", tool.Parallel.Policy},
			{"Idempotent", fmt.Sprint(tool.Parallel.Idempotent)},
			{"Exclusive workspace", fmt.Sprint(tool.Parallel.RequiresExclusiveWorkspace)},
			{"Cancellable", fmt.Sprint(tool.Parallel.Cancellable)},
			{"Rate limit key", tool.Parallel.RateLimitKey},
			{"Max concurrency", fmt.Sprint(tool.Parallel.MaxConcurrency)},
		}
		b.WriteString(markdownTable([]string{"Property", "Value"}, rows))
		b.WriteString("\nSchema:\n\n")
		b.WriteString("```json\n")
		b.WriteString(fencedJSON(tool.Schema))
		b.WriteString("\n```\n\n")
	}
	b.WriteString("## MCP Policy Appendix\n\n")
	b.WriteString("Static docsgen does not connect to MCP servers. It documents the local policy fields that classify remote tools before they enter the runtime registry.\n\n")
	b.WriteString(markdownTable([]string{"MCPServer field", "Description"}, mcpRows(data.MCP)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func toolsReferenceInput() toolsReferenceData {
	registry := tools.NewRegistry(config.BuiltIn())
	specs := registry.Specs()
	toolDocs := make([]toolDoc, 0, len(specs))
	for _, spec := range specs {
		decision := registry.PolicyDecision(spec.Name)
		meta, _ := registry.ParallelMetadata(spec.Name)
		riskClass := protocol.RiskClass(spec.Risk)
		toolDocs = append(toolDocs, toolDoc{
			Name:        spec.Name,
			Description: spec.Description,
			Risk:        string(spec.Risk),
			RiskClass:   string(riskClass),
			Decision:    decision.Decision + "/" + decision.Reason,
			Parallel:    meta,
			Schema:      append(json.RawMessage(nil), spec.Parameters...),
		})
	}
	sort.Slice(toolDocs, func(i, j int) bool { return toolDocs[i].Name < toolDocs[j].Name })
	return toolsReferenceData{
		Risks: protocol.RiskClassSpecs(),
		Tools: toolDocs,
		MCP:   mcpFieldDocs(),
	}
}

func riskRows(specs []protocol.RiskClassSpec) [][]string {
	rows := make([][]string, 0, len(specs))
	for _, spec := range specs {
		rows = append(rows, []string{string(spec.Risk), spec.Description})
	}
	return rows
}

func mcpRows(docs []mcpFieldDoc) [][]string {
	rows := make([][]string, 0, len(docs))
	for _, doc := range docs {
		rows = append(rows, []string{doc.Field, doc.Description})
	}
	return rows
}

func mcpFieldDocs() []mcpFieldDoc {
	descriptions := map[string]string{
		"Name":                     "Operator-defined server name and MCP tool namespace",
		"Command":                  "Stdio command used to start a local MCP server",
		"Args":                     "Command arguments for stdio MCP servers",
		"Env":                      "Literal environment variables passed to the server",
		"EnvVars":                  "Environment variable names copied from Billyharness env files",
		"CWD":                      "Working directory for stdio MCP server startup",
		"URL":                      "Streamable HTTP MCP URL; currently recorded as unsupported",
		"UnsupportedReason":        "Reason a configured server is visible but not runnable",
		"BearerTokenEnvVar":        "Environment variable for HTTP bearer auth when supported",
		"HTTPHeaders":              "Static HTTP headers for HTTP MCP when supported",
		"EnvHTTPHeaders":           "HTTP headers sourced from environment variables",
		"StartupTimeout":           "Startup timeout for server initialization",
		"ToolTimeout":              "Per-tool call timeout",
		"Enabled":                  "Whether the server participates in runtime discovery",
		"Required":                 "Whether missing startup should be treated as required",
		"EnabledTools":             "Side-effect allowlist and visibility allowlist for remote tools",
		"DisabledTools":            "Remote tool names hidden from the registry",
		"DefaultToolRisk":          "Default risk used when a remote tool lacks a per-tool override",
		"ToolRisks":                "Per-tool risk override map, parsed by protocol.ParseRisk",
		"DefaultToolsApprovalMode": "Compatibility approval mode metadata",
	}
	t := reflect.TypeOf(config.MCPServer{})
	out := make([]mcpFieldDoc, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i).Name
		out = append(out, mcpFieldDoc{Field: field, Description: descriptions[field]})
	}
	return out
}

func ToolReferenceNames() []string {
	data := toolsReferenceInput()
	names := make([]string, 0, len(data.Tools))
	for _, tool := range data.Tools {
		names = append(names, tool.Name)
	}
	return sortedStrings(names)
}

func toolHeading(name string) string {
	return "### " + strings.TrimSpace(name)
}
