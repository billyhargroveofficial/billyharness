package docsgen

import (
	"bytes"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
	"github.com/billyhargroveofficial/billyharness/internal/serviceops"
)

type cliReferenceData struct {
	Commands     []clientux.CLICommandDoc
	DoctorChecks []clientux.DoctorCheckDoc
}

func GenerateCLI() ([]byte, error) {
	data := cliReferenceInput()
	var b bytes.Buffer
	b.Write(header("internal/clientux, internal/serviceops, internal/docsgen"))
	b.WriteString("# CLI Reference\n\n")
	b.WriteString("This reference documents the top-level command table. Run `fast-agent-harness <command> -h` for command-specific flags; those flags stay owned by each command's FlagSet.\n\n")
	b.WriteString("## Top-Level Commands\n\n")
	b.WriteString(markdownTable([]string{"Name", "Aliases", "Summary"}, cliCommandRows(data.Commands)))
	b.WriteString("\n## What Doctor Checks\n\n")
	b.WriteString("Doctor check names come from the descriptor table that `doctor` ranges over at runtime. Checks marked `deep` run when `-deep` enables them, and generated-doc checks also run when `-docs` is passed explicitly.\n\n")
	b.WriteString(markdownTable([]string{"Name", "Modes", "Description"}, doctorCheckRows(data.DoctorChecks)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func cliReferenceInput() cliReferenceData {
	return cliReferenceData{
		Commands:     clientux.CLICommandDocs(),
		DoctorChecks: clientux.DoctorCheckDocs(cliDoctorCheckDocInput()),
	}
}

func cliDoctorCheckDocInput() clientux.DoctorCheckDocInput {
	targets := Targets()
	targetNames := make([]string, 0, len(targets))
	for _, target := range targets {
		targetNames = append(targetNames, target.Name)
	}
	services := serviceops.ManagedServices()
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

func cliCommandRows(commands []clientux.CLICommandDoc) [][]string {
	rows := make([][]string, 0, len(commands))
	for _, command := range commands {
		rows = append(rows, []string{
			command.Name,
			strings.Join(command.Aliases, ", "),
			command.Summary,
		})
	}
	return rows
}

func doctorCheckRows(checks []clientux.DoctorCheckDoc) [][]string {
	rows := make([][]string, 0, len(checks))
	for _, check := range checks {
		rows = append(rows, []string{
			check.Name,
			strings.Join(check.Modes, ", "),
			check.Description,
		})
	}
	return rows
}
