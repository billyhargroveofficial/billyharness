package main

import (
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

func agentClubConfigFilesFor(cfg config.Config) []string {
	if len(cfg.AgentClubConfigFiles) > 0 {
		return append([]string(nil), cfg.AgentClubConfigFiles...)
	}
	return config.DefaultAgentClubConfigFiles()
}

func loadAgentClubRegistryForConfig(cfg *config.Config) (*agentclub.Registry, gatewayapi.AgentClubReadinessStatus, error) {
	files := agentClubConfigFilesFor(*cfg)
	cfg.AgentClubConfigFiles = append([]string(nil), files...)
	loaded, err := agentclub.LoadConfigFiles(agentclub.LoadConfigOptions{
		Files:        files,
		SecretLookup: agentClubSecretLookup,
	})
	if err != nil {
		return nil, gatewayapi.AgentClubReadinessStatus{}, err
	}
	return loaded.Registry, agentClubReadinessStatus(loaded.Status), nil
}

func agentClubSecretLookup(name string) (string, bool) {
	return config.LookupEnvOrDotenv(strings.TrimSpace(name))
}

func agentClubReadinessStatus(status agentclub.ConfigStatus) gatewayapi.AgentClubReadinessStatus {
	return gatewayapi.AgentClubReadinessStatus{
		ConfiguredFileCount:    status.ConfiguredFileCount,
		ConfiguredFileBasename: append([]string(nil), status.ConfiguredFileBasename...),
		CapabilityCount:        status.CapabilityCount,
		BindingCount:           status.BindingCount,
		EnabledBindingCount:    status.EnabledBindingCount,
		TriggerCount:           status.TriggerCount,
		EnabledTriggerCount:    status.EnabledTriggerCount,
		EnabledAutoRunCount:    status.EnabledAutoRunCount,
		HMACSecretEnvCount:     status.HMACSecretEnvCount,
		MissingSecretEnvCount:  status.MissingSecretEnvCount,
		Configured:             status.ConfiguredFileCount > 0,
	}
}
