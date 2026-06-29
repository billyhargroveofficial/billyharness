package discovery

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	SourceNative = "native"
	SourceMCP    = "mcp"

	DefaultSchemaTokens = 1200
	MaxSchemaTokens     = 4000
)

type Candidate struct {
	Spec      protocol.ToolSpec
	Source    string
	Namespace string
	Server    string
	CallTool  string
	CallName  string
}

type Query struct {
	Query           string
	Server          string
	Namespace       string
	Risk            string
	Limit           int
	IncludeSchema   bool
	MaxSchemaTokens int
}

type Results struct {
	Items     []Item
	Truncated bool
	Metrics   Metrics
}

type Item struct {
	Name                string          `json:"name"`
	Source              string          `json:"source"`
	Namespace           string          `json:"namespace,omitempty"`
	Server              string          `json:"server,omitempty"`
	CallTool            string          `json:"call_tool"`
	CallName            string          `json:"call_name,omitempty"`
	Risk                protocol.Risk   `json:"risk,omitempty"`
	Description         string          `json:"description,omitempty"`
	InputSchema         json.RawMessage `json:"input_schema,omitempty"`
	SchemaOmittedReason string          `json:"schema_omitted,omitempty"`
}

type Metrics struct {
	DiscoveryCalls     int     `json:"discovery_calls"`
	ScannedNative      int     `json:"scanned_native"`
	ScannedMCP         int     `json:"scanned_mcp"`
	Matched            int     `json:"matched"`
	Returned           int     `json:"returned"`
	SchemaIncluded     int     `json:"schema_included"`
	SchemaOmitted      int     `json:"schema_omitted"`
	SchemaTokens       int     `json:"schema_tokens"`
	SchemaBudgetTokens int     `json:"schema_budget_tokens,omitempty"`
	SchemaTruncated    bool    `json:"schema_truncated,omitempty"`
	Filters            Filters `json:"filters"`
}

type Filters struct {
	Query     string `json:"query,omitempty"`
	Server    string `json:"server,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Risk      string `json:"risk,omitempty"`
}

func (m Metrics) Metadata() map[string]any {
	return map[string]any{
		"discovery_calls":      m.DiscoveryCalls,
		"matches":              m.Returned,
		"matched":              m.Matched,
		"truncated":            m.Returned < m.Matched || m.SchemaTruncated,
		"scanned_native":       m.ScannedNative,
		"scanned_mcp":          m.ScannedMCP,
		"schema_included":      m.SchemaIncluded,
		"schema_omitted":       m.SchemaOmitted,
		"schema_tokens":        m.SchemaTokens,
		"schema_budget_tokens": m.SchemaBudgetTokens,
		"schema_truncated":     m.SchemaTruncated,
	}
}

func Search(candidates []Candidate, q Query) Results {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	query := strings.ToLower(strings.TrimSpace(q.Query))
	serverFilter := NormalizeMCPServerFilter(q.Server)
	if serverFilter == "" && IsParilkaAlias(query) {
		serverFilter = "telegram_parilka"
		query = ""
	}
	namespaceFilter := NormalizeNamespaceFilter(q.Namespace)
	riskFilter := NormalizeRiskFilter(q.Risk)
	schemaBudget := 0
	if q.IncludeSchema {
		schemaBudget = NormalizeSchemaBudget(q.MaxSchemaTokens)
	}
	metrics := Metrics{
		DiscoveryCalls:     1,
		SchemaBudgetTokens: schemaBudget,
		Filters: Filters{
			Query:     query,
			Server:    DisplayMCPServerName(serverFilter),
			Namespace: namespaceFilter,
			Risk:      string(riskFilter),
		},
	}

	items := make([]Item, 0, min(q.Limit, len(candidates)))
	truncated := false
	for _, candidate := range candidates {
		if candidate.Source == SourceNative {
			if serverFilter != "" {
				continue
			}
			metrics.ScannedNative++
		}
		if candidate.Source == SourceMCP {
			serverName := NormalizeMCPServerFilter(candidate.Server)
			if serverFilter != "" && serverName != serverFilter {
				continue
			}
			metrics.ScannedMCP++
		}

		item := Item{
			Name:      candidate.Spec.Name,
			Source:    candidate.Source,
			Namespace: candidate.Namespace,
			Server:    displayCandidateServer(candidate),
			CallTool:  candidate.CallTool,
			CallName:  candidate.CallName,
		}
		if namespaceFilter != "" && !NamespaceMatches(item, namespaceFilter) {
			continue
		}
		if riskFilter != "" && candidate.Spec.Risk != riskFilter {
			continue
		}
		haystack := strings.ToLower(candidate.Spec.Name + " " + candidate.Spec.Description + " " + item.Server + " " + item.Namespace + " " + string(candidate.Spec.Risk) + " " + item.Source)
		if !Matches(haystack, query) {
			continue
		}
		metrics.Matched++
		if len(items) >= q.Limit {
			truncated = true
			break
		}
		item.Description = truncate(oneLine(candidate.Spec.Description), 240)
		item.Risk = candidate.Spec.Risk
		if q.IncludeSchema {
			AddSchemaWithinBudget(&item, candidate.Spec.Parameters, &metrics)
		}
		items = append(items, item)
		metrics.Returned = len(items)
	}

	return Results{Items: items, Truncated: truncated || metrics.SchemaTruncated, Metrics: metrics}
}

func AddSchemaWithinBudget(item *Item, schema json.RawMessage, metrics *Metrics) {
	if len(schema) == 0 {
		return
	}
	tokens := estimateTokens(string(schema))
	remaining := metrics.SchemaBudgetTokens - metrics.SchemaTokens
	if metrics.SchemaBudgetTokens <= 0 || tokens > remaining {
		item.SchemaOmittedReason = fmt.Sprintf("schema budget exceeded: need %d tokens, remaining %d", tokens, maxInt(0, remaining))
		metrics.SchemaOmitted++
		metrics.SchemaTruncated = true
		return
	}
	item.InputSchema = schema
	metrics.SchemaIncluded++
	metrics.SchemaTokens += tokens
}

func NormalizeSchemaBudget(tokens int) int {
	if tokens <= 0 {
		return DefaultSchemaTokens
	}
	if tokens > MaxSchemaTokens {
		return MaxSchemaTokens
	}
	return tokens
}

func NormalizeRiskFilter(value string) protocol.Risk {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "", "any", "all":
		return ""
	case "read", "readonly", "read_only":
		return protocol.RiskReadOnly
	case "network":
		return protocol.RiskNetwork
	case "write":
		return protocol.RiskWrite
	case "exec", "execute":
		return protocol.RiskExecute
	case "external", "mcp":
		return protocol.RiskExternal
	default:
		return protocol.Risk(value)
	}
}

func NormalizeNamespaceFilter(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.Trim(value, ".- ")
	if IsParilkaAlias(value) {
		return "telegram-parilka"
	}
	return value
}

func NativeNamespace(name string) string {
	switch {
	case strings.HasPrefix(name, "fs_"):
		return "fs"
	case strings.HasPrefix(name, "web_"):
		return "web"
	case strings.HasPrefix(name, "shell_"):
		return "shell"
	case strings.HasPrefix(name, "mcp_"):
		return "mcp-gateway"
	case strings.HasPrefix(name, "tool_"):
		return "tool"
	}
	prefix, _, ok := strings.Cut(name, "_")
	if ok {
		return prefix
	}
	return name
}

func MCPNamespace(server string) string {
	server = DisplayMCPServerName(NormalizeMCPServerFilter(server))
	if server == "" {
		return "mcp"
	}
	return "mcp." + server
}

func NamespaceMatches(item Item, filter string) bool {
	namespace := NormalizeNamespaceFilter(item.Namespace)
	server := NormalizeNamespaceFilter(item.Server)
	if filter == SourceNative {
		return item.Source == SourceNative
	}
	if filter == SourceMCP {
		return item.Source == SourceMCP || strings.HasPrefix(namespace, "mcp")
	}
	if namespace == filter || server == filter {
		return true
	}
	return strings.TrimPrefix(namespace, "mcp.") == filter
}

func Matches(haystack, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	if strings.Contains(haystack, query) {
		return true
	}
	for _, term := range strings.Fields(query) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func MCPServerFromToolName(name string) string {
	name = strings.TrimPrefix(name, "mcp__")
	server, _, ok := strings.Cut(name, "__")
	if !ok {
		return ""
	}
	return server
}

func NormalizeMCPServerFilter(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if IsParilkaAlias(value) {
		return "telegram_parilka"
	}
	return SanitizeMCPServerName(value)
}

func DisplayMCPServerName(value string) string {
	if value == "telegram_parilka" {
		return "telegram-parilka"
	}
	return value
}

func IsParilkaAlias(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "parilka") ||
		strings.Contains(value, "парил")
}

func SanitizeMCPServerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = mcpServerUnsafeRE.ReplaceAllString(value, "_")
	return strings.Trim(value, "_")
}

func displayCandidateServer(candidate Candidate) string {
	if candidate.Source != SourceMCP {
		return ""
	}
	return DisplayMCPServerName(NormalizeMCPServerFilter(candidate.Server))
}

func estimateTokens(text string) int {
	chars := len([]rune(text))
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("...[truncated %d bytes]", len(s)-n)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var mcpServerUnsafeRE = regexp.MustCompile(`[^a-z0-9_]+`)
