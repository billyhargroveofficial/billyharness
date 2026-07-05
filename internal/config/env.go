package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	EnvValueSourceEnvironment = "env"
	EnvValueSourceDotenv      = "dotenv"
)

type EnvValueSource struct {
	Kind string
	Key  string
	Path string
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

func envList(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func envListDefault(key string, fallback []string) []string {
	if value := envList(key); len(value) > 0 {
		return value
	}
	return append([]string(nil), fallback...)
}

func LookupEnvOrDotenv(key string) (string, bool) {
	value, _, ok := LookupEnvOrDotenvSource(key)
	return value, ok
}

func LookupEnvOrDotenvSource(key string) (string, EnvValueSource, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", EnvValueSource{}, false
	}
	if value, ok := os.LookupEnv(key); ok {
		if strings.TrimSpace(value) != "" {
			return value, EnvValueSource{Kind: EnvValueSourceEnvironment, Key: key}, true
		}
	}
	for _, path := range findDotenvFiles() {
		if value, ok := dotenvValueFromFile(path, key); ok && strings.TrimSpace(value) != "" {
			return value, EnvValueSource{Kind: EnvValueSourceDotenv, Key: key, Path: path}, true
		}
	}
	return "", EnvValueSource{}, false
}

func LookupEnvDotenvOrFiles(key string, extraFiles []string) (string, string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value, "environment", true
	}
	for _, path := range findDotenvFiles() {
		if value, ok := dotenvValueFromFile(path, key); ok && strings.TrimSpace(value) != "" {
			return value, ".env", true
		}
	}
	for _, path := range extraFiles {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" {
			continue
		}
		if value, ok := dotenvValueFromFile(path, key); ok && strings.TrimSpace(value) != "" {
			return value, "configured_env_file", true
		}
	}
	return "", "", false
}

func dotenvValue(key string) string {
	for _, path := range findDotenvFiles() {
		if value, ok := dotenvValueFromFile(path, key); ok {
			return value
		}
	}
	return ""
}

func dotenvValueFromFile(path, key string) (string, bool) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", false
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
		return strings.Trim(strings.TrimSpace(value), `"'`), true
	}
	return "", false
}

func findDotenv() string {
	files := findDotenvFiles()
	if len(files) == 0 {
		return ""
	}
	return files[0]
}

func DefaultDotenvPath() string {
	return filepath.Join(BillyHomeDir(), ".env")
}

func EffectiveDotenvPath() string {
	if !dotenvHomeOnly() {
		if explicit := strings.TrimSpace(os.Getenv("FAST_AGENT_ENV_FILE")); explicit != "" {
			return filepath.Clean(explicit)
		}
	}
	return DefaultDotenvPath()
}

func EffectiveWritableDotenvPath() (string, error) {
	path := EffectiveDotenvPath()
	if err := validateWritableDotenvPath(path); err != nil {
		return path, err
	}
	return path, nil
}

func findDotenvFiles() []string {
	seen := map[string]bool{}
	var files []string
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || seen[path] {
			return
		}
		seen[path] = true
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	add(DefaultDotenvPath())
	if dotenvHomeOnly() {
		return files
	}
	if explicit := strings.TrimSpace(os.Getenv("FAST_AGENT_ENV_FILE")); explicit != "" {
		return []string{explicit}
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			add(filepath.Join(dir, ".env"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return files
}

func dotenvHomeOnly() bool {
	return envBool("BILLYHARNESS_DOTENV_HOME_ONLY", false)
}

func validateWritableDotenvPath(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return fmt.Errorf("active dotenv path is empty")
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("active dotenv path %s is a directory", path)
		}
		if info.Mode().Perm()&0o222 == 0 {
			return fmt.Errorf("active dotenv path %s is read-only", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat active dotenv path %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	existing := dir
	for {
		info, err := os.Stat(existing)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("active dotenv parent %s is not a directory", existing)
			}
			if info.Mode().Perm()&0o222 == 0 {
				return fmt.Errorf("active dotenv parent %s is read-only for %s", existing, path)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat active dotenv parent %s for %s: %w", existing, path, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return fmt.Errorf("active dotenv parent %s does not exist for %s", dir, path)
		}
		existing = parent
	}
}

func BillyHomeDir() string {
	if explicit := os.Getenv("BILLYHARNESS_HOME"); explicit != "" {
		return explicit
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".billyharness"
	}
	return filepath.Join(home, "billyharness")
}
