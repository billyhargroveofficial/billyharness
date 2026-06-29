package mcpclient

import (
	"os"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

var defaultEnvVars = []string{
	"HOME",
	"LOGNAME",
	"PATH",
	"SHELL",
	"TMPDIR",
	"TEMP",
	"TMP",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"USER",
	"USERNAME",
	"APPDATA",
	"LOCALAPPDATA",
	"PROGRAMDATA",
	"SystemRoot",
	"COMSPEC",
}

func mcpEnv(server config.MCPServer) []string {
	env := make([]string, 0, len(defaultEnvVars)+len(server.EnvVars)+len(server.Env))
	for _, name := range defaultEnvVars {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	for _, name := range server.EnvVars {
		if value, ok := config.LookupEnvOrDotenv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	keys := make([]string, 0, len(server.Env))
	for key := range server.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+server.Env[key])
	}
	return env
}

func serverSecrets(server config.MCPServer) []string {
	var values []string
	for key, value := range server.Env {
		if value == "" || len(value) < 8 {
			continue
		}
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "apikey") {
			values = append(values, value)
		}
	}
	for _, name := range server.EnvVars {
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "token") &&
			!strings.Contains(lower, "secret") &&
			!strings.Contains(lower, "password") &&
			!strings.Contains(lower, "api_key") &&
			!strings.Contains(lower, "apikey") {
			continue
		}
		if value, ok := config.LookupEnvOrDotenv(name); ok && len(value) >= 8 {
			values = append(values, value)
		}
	}
	return values
}
