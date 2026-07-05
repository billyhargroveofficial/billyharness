package docsgen

import (
	"fmt"
	"slices"
)

type Target struct {
	Name        string
	Filename    string
	Generate    func() ([]byte, error)
	Fingerprint func() (string, error)
}

func Targets() []Target {
	targets := []Target{
		{Name: "cli", Filename: "cli.md", Generate: GenerateCLI, Fingerprint: fingerprintCLI},
		{Name: "commands", Filename: "commands.md", Generate: GenerateCommands, Fingerprint: fingerprintCommands},
		{Name: "config", Filename: "config.md", Generate: GenerateConfig, Fingerprint: fingerprintConfig},
		{Name: "events", Filename: "events.md", Generate: GenerateEvents, Fingerprint: fingerprintEvents},
		{Name: "gateway-api", Filename: "gateway-api.md", Generate: GenerateGatewayAPI, Fingerprint: fingerprintGatewayAPI},
		{Name: "packages", Filename: "packages.md", Generate: GeneratePackages, Fingerprint: fingerprintPackages},
		{Name: "tools", Filename: "tools.md", Generate: GenerateTools, Fingerprint: fingerprintTools},
	}
	slices.SortFunc(targets, func(a, b Target) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return targets
}

func SelectTargets(only string) ([]Target, error) {
	if only == "" {
		return Targets(), nil
	}
	for _, target := range Targets() {
		if target.Name == only {
			return []Target{target}, nil
		}
	}
	return nil, fmt.Errorf("unknown docsgen target %q", only)
}
