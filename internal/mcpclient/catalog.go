package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type serverCatalog struct {
	runtime      *managedServer
	server       config.MCPServer
	specs        []protocol.ToolSpec
	prompts      []Prompt
	instructions string
}

type catalogToolCandidate struct {
	externalName string
	source       string
	originalName string
	spec         protocol.ToolSpec
	runtime      *managedServer
	server       config.MCPServer
}

func buildCatalog(catalogs []serverCatalog) ([]ExternalTool, []string, []string) {
	grouped := map[string][]catalogToolCandidate{}
	var instructions []string
	for _, catalog := range catalogs {
		if strings.TrimSpace(catalog.instructions) != "" {
			instructions = append(instructions, fmt.Sprintf("%s: %s", catalog.server.Name, truncateText(catalog.instructions, 512)))
		}
		for _, spec := range catalog.specs {
			originalName := spec.Name
			externalName := toolName(catalog.server.Name, originalName)
			if externalName == "" {
				continue
			}
			grouped[externalName] = append(grouped[externalName], catalogToolCandidate{
				externalName: externalName,
				source:       catalog.server.Name + "/" + originalName,
				originalName: originalName,
				spec:         spec,
				runtime:      catalog.runtime,
				server:       catalog.server,
			})
		}
	}

	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]ExternalTool, 0, len(names))
	var collisions []string
	for _, name := range names {
		candidates := grouped[name]
		if len(candidates) > 1 {
			sources := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				sources = append(sources, candidate.source)
			}
			collisions = append(collisions, fmt.Sprintf("MCP tool name collision: %s maps %s", name, strings.Join(sources, " and ")))
			continue
		}
		candidate := candidates[0]
		spec := candidate.spec
		spec.Name = candidate.externalName
		spec.Description = strings.TrimSpace(fmt.Sprintf("MCP %s/%s. %s", candidate.server.Name, candidate.originalName, spec.Description))
		spec.Risk = protocol.RiskExternal
		toolTimeout := candidate.server.ToolTimeout
		if toolTimeout <= 0 {
			toolTimeout = 300 * time.Second
		}
		runtime := candidate.runtime
		originalName := candidate.originalName
		tools = append(tools, ExternalTool{
			Spec: spec,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				callCtx, cancel := context.WithTimeout(ctx, toolTimeout)
				defer cancel()
				return runtime.callTool(callCtx, originalName, args)
			},
		})
	}
	return tools, instructions, collisions
}

func buildPromptCatalog(catalogs []serverCatalog) []Prompt {
	var prompts []Prompt
	for _, catalog := range catalogs {
		for _, prompt := range catalog.prompts {
			prompt.Server = catalog.server.Name
			prompt.Arguments = append([]PromptArgument(nil), prompt.Arguments...)
			prompts = append(prompts, prompt)
		}
	}
	sort.Slice(prompts, func(i, j int) bool {
		left := prompts[i].Server + "/" + prompts[i].Name
		right := prompts[j].Server + "/" + prompts[j].Name
		return left < right
	})
	return prompts
}

func toolAllowed(server config.MCPServer, name string) bool {
	if len(server.EnabledTools) > 0 && !contains(server.EnabledTools, name) {
		return false
	}
	if contains(server.DisabledTools, name) {
		return false
	}
	return true
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

var unsafeToolChars = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func toolName(server, tool string) string {
	name := "mcp__" + sanitize(server) + "__" + sanitize(tool)
	name = strings.Trim(name, "_")
	if name == "mcp" || name == "" {
		return ""
	}
	return name
}

func sanitize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, ".", "_")
	value = unsafeToolChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "server"
	}
	return value
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + fmt.Sprintf("...[truncated %d bytes]", len(text)-limit)
}
