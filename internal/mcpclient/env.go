package mcpclient

import (
	"net/url"
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
	values = append(values, urlCredentialSecrets(server.URL)...)
	values = append(values, argSecrets(server.Args)...)
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

func urlCredentialSecrets(rawURL string) []string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.User == nil {
		return nil
	}
	var values []string
	if username := u.User.Username(); len(username) >= 8 {
		values = append(values, username)
	}
	if password, ok := u.User.Password(); ok && len(password) >= 8 {
		values = append(values, password)
	}
	return values
}

func argSecrets(args []string) []string {
	var values []string
	for i, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if ok && tokenLikeName(key) && len(value) >= 8 {
			values = append(values, value)
			continue
		}
		if tokenLikeName(arg) && i+1 < len(args) && len(args[i+1]) >= 8 {
			values = append(values, args[i+1])
		}
	}
	return values
}

func tokenLikeName(value string) bool {
	value = strings.ToLower(strings.TrimLeft(strings.TrimSpace(value), "-/"))
	value = strings.ReplaceAll(value, "-", "_")
	return strings.Contains(value, "token") ||
		strings.Contains(value, "secret") ||
		strings.Contains(value, "password") ||
		strings.Contains(value, "api_key") ||
		strings.Contains(value, "apikey")
}
