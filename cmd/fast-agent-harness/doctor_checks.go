package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/docsgen"
	"github.com/billyhargroveofficial/billyharness/internal/serviceops"
)

type doctorContext struct {
	Context context.Context
	Config  config.Config
	Runtime doctorRuntimeStatus
	RepoDir string
	Options doctorOptions
	Runner  doctorCommandRunner
}

type doctorCheckSpec struct {
	Name        string
	Description string
	Modes       []string
	Run         func(*doctorContext) []doctorCheck
}

func doctorCheckSpecs() []doctorCheckSpec {
	specs, err := buildDoctorCheckSpecs(doctorCheckDocs(), doctorCheckRuns())
	if err != nil {
		panic(err)
	}
	return specs
}

func doctorCheckDocs() []clientux.DoctorCheckDoc {
	return clientux.DoctorCheckDocs(doctorCheckDocInput())
}

func doctorCheckDocInput() clientux.DoctorCheckDocInput {
	targets := docsgen.Targets()
	targetNames := make([]string, 0, len(targets))
	for _, target := range targets {
		targetNames = append(targetNames, target.Name)
	}
	services := doctorManagedServices()
	serviceDocs := make([]clientux.DoctorManagedServiceDoc, 0, len(services))
	for _, service := range services {
		serviceDocs = append(serviceDocs, clientux.DoctorManagedServiceDoc{
			Service:    service.Service,
			Subcommand: service.Subcommand,
			PIDFile:    service.PIDFile,
		})
	}
	return clientux.DoctorCheckDocInput{
		DocsTargets:     targetNames,
		ManagedServices: serviceDocs,
	}
}

func buildDoctorCheckSpecs(docs []clientux.DoctorCheckDoc, runs map[string]func(*doctorContext) []doctorCheck) ([]doctorCheckSpec, error) {
	seenDocs := map[string]bool{}
	specs := make([]doctorCheckSpec, 0, len(docs))
	for _, doc := range docs {
		name := strings.TrimSpace(doc.Name)
		if name == "" {
			return nil, fmt.Errorf("doctor check doc missing name")
		}
		if seenDocs[name] {
			return nil, fmt.Errorf("duplicate doctor check doc %q", name)
		}
		run, ok := runs[name]
		if !ok || run == nil {
			return nil, fmt.Errorf("doctor check %q missing run func", name)
		}
		seenDocs[name] = true
		specs = append(specs, doctorCheckSpec{
			Name:        name,
			Description: strings.TrimSpace(doc.Description),
			Modes:       append([]string{}, doc.Modes...),
			Run:         run,
		})
	}
	for name := range runs {
		if !seenDocs[name] {
			return nil, fmt.Errorf("doctor check run func %q missing doc", name)
		}
	}
	return specs, nil
}

func doctorCheckRuns() map[string]func(*doctorContext) []doctorCheck {
	runs := map[string]func(*doctorContext) []doctorCheck{
		"config provider/model": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorEffectiveConfigCheck(ctx.Config)}
		},
		"provider capability": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorProviderCapabilityCheck(ctx.Config)}
		},
		"gateway bind address": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorGatewayBindCheck(ctx.Config)}
		},
		"auth configured": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorActiveAuthCheck(ctx.Runtime.Auth)}
		},
		"mcp allowlist": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorMCPAllowlistCheck(ctx.Config)}
		},
		"agentclub config": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorAgentClubConfigCheck(ctx.Runtime.AgentClub)}
		},
		"tool catalog": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorToolCatalogStatus(ctx.Config)}
		},
		"session store access": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorSessionStoreAccessCheck(ctx.Runtime.GatewaySessionStore.Path)}
		},
		"attachments store usage": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorAttachmentsStoreUsageCheck(ctx.Runtime.AttachmentsStore)}
		},
		"git status": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorGitStatus(ctx.Context, ctx.RepoDir, ctx.Options, ctx.Runner)}
		},
		"build check": func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorBuildStatus(ctx.Context, ctx.RepoDir, ctx.Options, ctx.Runner)}
		},
		"gateway /health": func(ctx *doctorContext) []doctorCheck {
			return doctorGatewayEndpointCheck(ctx, "/health", "gateway /health")
		},
		"gateway /ready": func(ctx *doctorContext) []doctorCheck {
			return doctorGatewayEndpointCheck(ctx, "/ready", "gateway /ready")
		},
	}
	for _, target := range docsgen.Targets() {
		target := target
		runs["docs:"+target.Name] = func(ctx *doctorContext) []doctorCheck {
			if !ctx.Options.CheckDocs {
				return nil
			}
			return []doctorCheck{doctorDocsTargetStatus(ctx.RepoDir, ctx.Options, target)}
		}
	}
	for _, service := range doctorManagedServices() {
		service := service
		runs["service "+service.Service] = func(ctx *doctorContext) []doctorCheck {
			return []doctorCheck{doctorServiceActiveStatus(ctx, service)}
		}
		runs["process "+service.Subcommand+" duplicates"] = func(ctx *doctorContext) []doctorCheck {
			if !ctx.Options.CheckServices {
				return nil
			}
			checks := doctorProcessDuplicateChecks(ctx.Context, ctx.Options, ctx.Runner, []serviceops.ManagedService{service})
			return checks
		}
		runs["pid file "+service.PIDFile] = func(ctx *doctorContext) []doctorCheck {
			if !ctx.Options.CheckServices {
				return nil
			}
			return doctorPIDFileChecks([]serviceops.ManagedService{service})
		}
		runs["service unit "+service.Service] = func(ctx *doctorContext) []doctorCheck {
			if !ctx.Options.CheckServices {
				return nil
			}
			return doctorServiceUnitMetadataChecks(ctx.Context, ctx.Options, ctx.Runner, []serviceops.ManagedService{service})
		}
		runs["service journal "+service.Service] = func(ctx *doctorContext) []doctorCheck {
			if !ctx.Options.CheckServices {
				return nil
			}
			return doctorServiceJournalChecks(ctx.Context, ctx.Options, ctx.Runner, []serviceops.ManagedService{service})
		}
	}
	return runs
}

func doctorCheckSpecEnabled(spec doctorCheckSpec, opts doctorOptions) bool {
	if len(spec.Modes) == 0 {
		return true
	}
	for _, mode := range spec.Modes {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case opts.Mode:
			return true
		case "deep":
			if opts.Deep || opts.CheckDocs {
				return true
			}
		}
	}
	return false
}

func doctorDocsTargetStatus(repoDir string, opts doctorOptions, target docsgen.Target) doctorCheck {
	for _, check := range doctorDocsStatuses(repoDir, opts) {
		if check.Name == "docs:"+target.Name {
			return check
		}
	}
	return doctorCheck{Name: "docs:" + target.Name, Status: "fail", Detail: "docs target missing from verifier"}
}

func doctorServiceActiveStatus(ctx *doctorContext, service serviceops.ManagedService) doctorCheck {
	if !ctx.Options.CheckServices {
		return doctorCheck{Name: "service " + service.Service, Status: "skip", Detail: "disabled"}
	}
	start := time.Now()
	cmdOut, err := runDoctorCommand(ctx.Context, ctx.Runner, "", ctx.Options.Timeout, "systemctl", "is-active", service.Service)
	check := doctorCheck{Name: "service " + service.Service, DurationMS: time.Since(start).Milliseconds()}
	state := strings.TrimSpace(cmdOut)
	switch {
	case err == nil && state == "active":
		check.Status = "ok"
		check.Detail = "active"
	case isCommandMissing(err):
		check.Status = "skip"
		check.Detail = "systemctl unavailable"
	default:
		check.Status = "fail"
		check.Detail = commandErrorDetail(cmdOut, err)
		if state != "" && !strings.Contains(check.Detail, state) {
			check.Detail = state + ": " + check.Detail
		}
	}
	return check
}

func doctorGatewayEndpointCheck(ctx *doctorContext, path string, name string) []doctorCheck {
	if !ctx.Options.CheckGateway {
		return []doctorCheck{{Name: name, Status: "skip", Detail: "disabled"}}
	}
	return []doctorCheck{doctorGatewayEndpointStatus(ctx.Context, ctx.Config, ctx.Options, path, name)}
}
