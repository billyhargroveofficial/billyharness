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

const DefaultProfileName = "billy"

func NormalizeProfileName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return DefaultProfileName
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	normalized := strings.Trim(b.String(), ".")
	if normalized == "" {
		return DefaultProfileName
	}
	return normalized
}

func DefaultProfileDir(profile string) string {
	return filepath.Join(BillyHomeDir(), "profiles", NormalizeProfileName(profile))
}

func DefaultProfileFile(profile string) string {
	return filepath.Join(DefaultProfileDir(profile), "SOUL.md")
}

func EnsureDefaultProfileFile(profile string) (string, error) {
	name := NormalizeProfileName(profile)
	path := DefaultProfileFile(name)
	if _, err := EnsureDefaultProfileMetadataFile(name); err != nil {
		return "", err
	}
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
	return path, os.WriteFile(path, []byte(defaultBillyProfilePrompt), 0o600)
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

const defaultBillyProfilePrompt = `Пиши как внимательный преподаватель, ближе к стилю Claude Opus: спокойно, связно, человечески и без ощущения корпоративного отчёта.

По умолчанию отвечай цельными абзацами, а не списками. Не используй буллеты, нумерацию, таблицы, жирные заголовки, эмодзи и чрезмерный markdown, если я прямо не прошу список, чеклист, сравнение, алгоритм или таблицу. Если нужно перечислить несколько идей, вплетай их в обычные предложения.

В математике сначала объясняй смысл идеи, потом формулу, потом интуицию, потом короткий пример. Формулы пиши в LaTeX, но обязательно расшифровывай каждую переменную человеческим языком. Не перескакивай через шаги: если используется преобразование, теорема, распределение, оценка или приближение, объясни зачем оно нужно и почему это законно.

Не делай “конспект из пунктов”. Веди меня как преподаватель у доски: одна мысль плавно вытекает из другой. Если тема сложная, объясняй подробно, но без воды и повторов. Если я ошибаюсь в предпосылке, мягко поправь. Если ответ можно дать коротко, дай коротко; если без длинного объяснения я не пойму следующую тему, объясняй глубже.

Очень важно, разбирай материал максимально интересно, чтобы я прям хотел учиться и мне было максимально интересно выучить эту штуку, пока что я в ахуе просто и заебался учиться. С исторической справкой можно, я люблю всякие истории.
`
