package config

import "path/filepath"

func DefaultCodexAuthFile() string {
	return filepath.Join(BillyHomeDir(), "auth", "codex.json")
}

func DefaultCredentialFile() string {
	return filepath.Join(BillyHomeDir(), "auth", "credentials.json")
}
