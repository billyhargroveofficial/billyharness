package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Provider              string
	Model                 string
	BaseURL               string
	APIKeyEnv             string
	Thinking              string
	ReasoningEffort       string
	MaxTokens             int
	MaxToolRounds         int
	RequestTimeout        time.Duration
	StreamIdleTimeout     time.Duration
	WorkspaceRoots        []string
	MaxToolOutputBytes    int
	AutoApproveDangerous  bool
	StoreReasoningContent bool
	GatewayAddr           string
}

func Default() Config {
	cwd, _ := os.Getwd()
	return Config{
		Provider:              env("FAST_AGENT_PROVIDER", "deepseek"),
		Model:                 env("FAST_AGENT_MODEL", "deepseek-v4-flash"),
		BaseURL:               env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		APIKeyEnv:             env("DEEPSEEK_API_KEY_ENV", "DEEPSEEK_API_KEY"),
		Thinking:              env("DEEPSEEK_THINKING", "enabled"),
		ReasoningEffort:       env("DEEPSEEK_REASONING_EFFORT", "high"),
		MaxTokens:             envInt("FAST_AGENT_MAX_TOKENS", 8192),
		MaxToolRounds:         envInt("FAST_AGENT_MAX_TOOL_ROUNDS", 8),
		RequestTimeout:        time.Duration(envInt("FAST_AGENT_REQUEST_TIMEOUT_SEC", 240)) * time.Second,
		StreamIdleTimeout:     time.Duration(envInt("FAST_AGENT_STREAM_IDLE_TIMEOUT_SEC", 60)) * time.Second,
		WorkspaceRoots:        []string{filepath.Clean(cwd)},
		MaxToolOutputBytes:    envInt("FAST_AGENT_MAX_TOOL_OUTPUT_BYTES", 64*1024),
		AutoApproveDangerous:  envBool("FAST_AGENT_AUTO_APPROVE_DANGEROUS", false),
		StoreReasoningContent: envBool("FAST_AGENT_STORE_REASONING", false),
		GatewayAddr:           env("FAST_AGENT_GATEWAY_ADDR", "127.0.0.1:8765"),
	}
}

func (c Config) APIKey() string {
	if c.Provider == "mock" {
		return ""
	}
	if value := os.Getenv(c.APIKeyEnv); value != "" {
		return value
	}
	return dotenvValue(c.APIKeyEnv)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func dotenvValue(key string) string {
	path := findDotenv()
	if path == "" {
		return ""
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}

func findDotenv() string {
	if explicit := os.Getenv("FAST_AGENT_ENV_FILE"); explicit != "" {
		return explicit
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		path := filepath.Join(dir, ".env")
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
