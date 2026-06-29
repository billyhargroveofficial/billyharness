package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type ProfileMetadata struct {
	Name                 string   `json:"name" toml:"name"`
	Provider             string   `json:"provider,omitempty" toml:"provider"`
	Model                string   `json:"model,omitempty" toml:"model"`
	Thinking             string   `json:"thinking,omitempty" toml:"thinking"`
	ReasoningEffort      string   `json:"reasoning_effort,omitempty" toml:"reasoning_effort"`
	DisableSpark         *bool    `json:"disable_spark,omitempty" toml:"disable_spark"`
	ContextWindowTokens  int64    `json:"context_window_tokens,omitempty" toml:"context_window_tokens"`
	WebSummaryMode       string   `json:"web_summary_mode,omitempty" toml:"web_summary_mode"`
	ToolPolicy           string   `json:"tool_policy,omitempty" toml:"tool_policy"`
	MCPAllowlist         []string `json:"mcp_allowlist,omitempty" toml:"mcp_allowlist"`
	InstructionFragments []string `json:"instruction_fragments,omitempty" toml:"instruction_fragments"`
	CostBudgetHints      []string `json:"cost_budget_hints,omitempty" toml:"cost_budget_hints"`
}

func DefaultProfileMetadataFile(profile string) string {
	return filepath.Join(DefaultProfileDir(profile), "profile.toml")
}

func EnsureDefaultProfileMetadataFile(profile string) (string, error) {
	name := NormalizeProfileName(profile)
	path := DefaultProfileMetadataFile(name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if name != DefaultProfileName {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(defaultBillyProfileMetadata), 0o600)
}

func LoadProfileMetadata(profile string) (ProfileMetadata, string, bool, error) {
	name := NormalizeProfileName(profile)
	path := DefaultProfileMetadataFile(name)
	var meta ProfileMetadata
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if meta, ok := builtInProfileMetadata(name); ok {
				return meta, "", true, nil
			}
			return ProfileMetadata{}, path, false, nil
		}
		return ProfileMetadata{}, path, false, err
	}
	if _, err := toml.DecodeFile(path, &meta); err != nil {
		return ProfileMetadata{}, path, false, err
	}
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = name
	} else {
		meta.Name = NormalizeProfileName(meta.Name)
	}
	meta.MCPAllowlist = cleanStringList(meta.MCPAllowlist)
	meta.InstructionFragments = cleanStringList(meta.InstructionFragments)
	meta.CostBudgetHints = cleanStringList(meta.CostBudgetHints)
	return meta, path, true, nil
}

func builtInProfileMetadata(profile string) (ProfileMetadata, bool) {
	name := NormalizeProfileName(profile)
	if name != DefaultProfileName {
		return ProfileMetadata{}, false
	}
	disableSpark := true
	return ProfileMetadata{
		Name:                 DefaultProfileName,
		Provider:             "deepseek",
		Model:                "deepseek-v4-flash",
		Thinking:             "enabled",
		ReasoningEffort:      "high",
		DisableSpark:         &disableSpark,
		ContextWindowTokens:  1_000_000,
		WebSummaryMode:       "extractive",
		ToolPolicy:           "solo-full-access",
		MCPAllowlist:         []string{"telegram", "telegram-parilka", "github", "context7"},
		InstructionFragments: []string{"SOUL.md"},
		CostBudgetHints:      []string{"Keep native extractive web summaries by default; use model web summaries only when explicitly enabled."},
	}, true
}

func (c *Config) ApplyProfileMetadata() error {
	meta, _, ok, err := LoadProfileMetadata(c.Profile)
	if err != nil || !ok {
		return err
	}
	if strings.TrimSpace(meta.Provider) != "" {
		c.Provider = strings.TrimSpace(meta.Provider)
	}
	if strings.TrimSpace(meta.Model) != "" {
		c.Model = strings.TrimSpace(meta.Model)
	}
	if strings.TrimSpace(meta.Thinking) != "" {
		c.Thinking = strings.TrimSpace(meta.Thinking)
	}
	if strings.TrimSpace(meta.ReasoningEffort) != "" {
		c.ReasoningEffort = strings.TrimSpace(meta.ReasoningEffort)
	}
	if meta.DisableSpark != nil {
		c.DisableSpark = *meta.DisableSpark
	}
	if meta.ContextWindowTokens > 0 {
		c.ContextWindowTokens = meta.ContextWindowTokens
	}
	if strings.TrimSpace(meta.WebSummaryMode) != "" {
		c.WebSummaryMode = strings.TrimSpace(meta.WebSummaryMode)
	}
	if len(meta.MCPAllowlist) > 0 {
		c.MCPAllowedServers = append([]string(nil), meta.MCPAllowlist...)
	}
	c.ApplyModelProviderDefaults()
	c.ApplyWebSummaryDefaults()
	return nil
}

const defaultBillyProfileMetadata = `name = "billy"
provider = "deepseek"
model = "deepseek-v4-flash"
thinking = "enabled"
reasoning_effort = "high"
disable_spark = true
context_window_tokens = 1000000
web_summary_mode = "extractive"
tool_policy = "solo-full-access"
mcp_allowlist = ["telegram", "telegram-parilka", "github", "context7"]
instruction_fragments = ["SOUL.md"]
cost_budget_hints = ["Keep native extractive web summaries by default; use model web summaries only when explicitly enabled."]
`
