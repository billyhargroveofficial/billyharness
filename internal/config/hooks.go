package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Hook struct {
	Name           string
	Event          string
	Command        string
	Args           []string
	Env            map[string]string
	EnvVars        []string
	CWD            string
	Timeout        time.Duration
	MaxOutputBytes int
	Fatal          bool
	Enabled        bool
}

type hooksConfig struct {
	Hooks map[string]map[string]hookTOML `toml:"hooks"`
}

type hookTOML struct {
	Command        string            `toml:"command"`
	Args           []string          `toml:"args"`
	Env            map[string]string `toml:"env"`
	EnvVars        mcpEnvVars        `toml:"env_vars"`
	CWD            string            `toml:"cwd"`
	TimeoutSec     float64           `toml:"timeout_sec"`
	MaxOutputBytes int               `toml:"max_output_bytes"`
	Fatal          bool              `toml:"fatal"`
	Enabled        *bool             `toml:"enabled"`
}

func (c *Config) LoadDefaultHooks() error {
	if !c.HooksEnabled {
		c.Hooks = nil
		return nil
	}
	files := c.HookConfigFiles
	if len(files) == 0 {
		files = DefaultHookConfigFiles()
	}
	hooks, err := LoadHooks(files)
	if err != nil {
		return err
	}
	c.HookConfigFiles = files
	c.Hooks = hooks
	return nil
}

func DefaultHookConfigFiles() []string {
	path := DefaultHookConfigFile()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{path}
}

func DefaultHookConfigFile() string {
	return filepath.Join(BillyHomeDir(), "hooks.config.toml")
}

func LoadHooks(files []string) ([]Hook, error) {
	merged := map[string]Hook{}
	var keys []string
	for _, file := range files {
		if strings.TrimSpace(file) == "" {
			continue
		}
		var root hooksConfig
		if _, err := toml.DecodeFile(file, &root); err != nil {
			return nil, err
		}
		for rawEvent, hooksByName := range root.Hooks {
			event := normalizeHookEvent(rawEvent)
			if !validHookEvent(event) {
				return nil, fmt.Errorf("hooks.%s: unsupported hook event", rawEvent)
			}
			names := make([]string, 0, len(hooksByName))
			for name := range hooksByName {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				raw := hooksByName[name]
				hook, ok, err := raw.toHook(event, name)
				if err != nil {
					return nil, err
				}
				key := event + "/" + hook.Name
				if !ok {
					delete(merged, key)
					keys = removeString(keys, key)
					continue
				}
				if _, exists := merged[key]; !exists {
					keys = append(keys, key)
				}
				merged[key] = hook
			}
		}
	}
	out := make([]Hook, 0, len(merged))
	for _, key := range keys {
		if hook, ok := merged[key]; ok {
			out = append(out, hook)
		}
	}
	return out, nil
}

func (h hookTOML) toHook(event, name string) (Hook, bool, error) {
	enabled := true
	if h.Enabled != nil {
		enabled = *h.Enabled
	}
	if !enabled {
		return Hook{}, false, nil
	}
	if strings.TrimSpace(name) == "" {
		return Hook{}, false, fmt.Errorf("hooks.%s: hook name required", event)
	}
	if strings.TrimSpace(h.Command) == "" {
		return Hook{}, false, fmt.Errorf("hooks.%s.%s: command required", event, name)
	}
	timeout := time.Duration(h.TimeoutSec * float64(time.Second))
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	maxOutput := h.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 4096
	}
	if maxOutput > 64*1024 {
		maxOutput = 64 * 1024
	}
	return Hook{
		Name:           normalizeHookName(name),
		Event:          event,
		Command:        strings.TrimSpace(h.Command),
		Args:           append([]string(nil), h.Args...),
		Env:            cloneStringMap(h.Env),
		EnvVars:        append([]string(nil), h.EnvVars...),
		CWD:            strings.TrimSpace(h.CWD),
		Timeout:        timeout,
		MaxOutputBytes: maxOutput,
		Fatal:          h.Fatal,
		Enabled:        true,
	}, true, nil
}

func normalizeHookName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Trim(value, "_")
}

func normalizeHookEvent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Trim(value, "_")
}

func validHookEvent(event string) bool {
	switch event {
	case "session_start", "user_prompt_submit", "before_tool", "after_tool", "mcp_status_change", "provider_retry", "session_done":
		return true
	default:
		return false
	}
}
