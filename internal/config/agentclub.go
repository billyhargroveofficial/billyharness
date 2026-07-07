package config

import (
	"os"
	"path/filepath"
)

func DefaultAgentClubConfigFiles() []string {
	path := DefaultAgentClubConfigFile()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return []string{path}
}

func DefaultAgentClubConfigFile() string {
	return filepath.Join(BillyHomeDir(), "agentclub.config.json")
}
