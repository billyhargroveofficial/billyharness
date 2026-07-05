package docsgen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

type configReferenceData struct {
	Sources  []configSourceDoc
	Keys     []config.ConfigKeySpec
	Settings []config.BillySettingsFieldSpec
	Outside  []configOutsideDoc
}

type configSourceDoc struct {
	Layer       string
	Description string
}

type configOutsideDoc struct {
	Item        string
	Description string
}

func GenerateConfig() ([]byte, error) {
	data := configReferenceInput()
	var b bytes.Buffer
	b.Write(header("internal/config"))
	b.WriteString("# Config Reference\n\n")
	b.WriteString("## Precedence\n\n")
	b.WriteString(markdownTable([]string{"Layer", "Description"}, configSourceRows(data.Sources)))
	b.WriteString("\n## Keys\n\n")
	b.WriteString(markdownTable([]string{"Key", "Type", "Default", "Env aliases", "Redacted", "Description"}, configKeyRows(data.Keys)))
	b.WriteString("\n## settings.json\n\n")
	b.WriteString("This file uses `config.BillySettings` as the canonical JSON shape. It stores TUI display preferences plus the saved model/profile/context choices that config resolution reads from the settings layer.\n\n")
	b.WriteString(markdownTable([]string{"JSON key", "Go field", "Type", "Optional", "Written by"}, configSettingsRows(data.Settings)))
	b.WriteString("\n## Outside The Key Table\n\n")
	b.WriteString(markdownTable([]string{"Item", "Description"}, configOutsideRows(data.Outside)))
	footer, err := sourceHashFooter(data)
	if err != nil {
		return nil, err
	}
	b.Write(footer)
	return b.Bytes(), nil
}

func configReferenceInput() configReferenceData {
	return configReferenceData{
		Sources:  configSourceDocs(),
		Keys:     config.ConfigKeySpecs(),
		Settings: config.BillySettingsFieldSpecs(),
		Outside: []configOutsideDoc{
			{Item: "workspace_roots", Description: "Computed from the current working directory, not read from configSpecs()"},
			{Item: "$BILLYHARNESS_HOME/mcp.config.toml", Description: "MCP sub-registry loaded by " + homeRelative(config.DefaultMCPConfigFile())},
			{Item: "$BILLYHARNESS_HOME/hooks.config.toml", Description: "Hook sub-registry loaded by " + homeRelative(config.DefaultHookConfigFile())},
			{Item: "$BILLYHARNESS_HOME/diagnostics.config.toml", Description: "Diagnostics sub-registry loaded by " + homeRelative(config.DefaultDiagnosticsConfigFile())},
		},
	}
}

func configSourceDocs() []configSourceDoc {
	// Keep this in the same order as loadResolveState/resolve applies layers.
	return []configSourceDoc{
		{Layer: config.SourceBuiltIn, Description: "Built-in defaults from internal/config/defaults.go"},
		{Layer: config.SourceHomeConfig, Description: "Home config.toml overrides built-ins"},
		{Layer: config.SourceProject, Description: "Nearest project .billyharness/config.toml overrides trusted runtime keys"},
		{Layer: config.SourceSettings, Description: "settings.json applies saved model, profile, and context choices"},
		{Layer: config.SourceDotenv, Description: ".env values override file-backed config"},
		{Layer: config.SourceEnvironment, Description: "Process environment overrides dotenv and files"},
		{Layer: config.SourceCLI, Description: "Command-line flags override loaded config"},
		{Layer: config.SourceGateway, Description: "Gateway and TUI runtime overrides share CLI-level precedence"},
		{Layer: config.SourceProfile, Description: "Selected profile metadata applies after explicit selection"},
		{Layer: config.SourceDerived, Description: "Derived normalized values are recorded after model/provider defaults"},
	}
}

func configSourceRows(docs []configSourceDoc) [][]string {
	rows := make([][]string, 0, len(docs))
	for _, doc := range docs {
		rows = append(rows, []string{doc.Layer, doc.Description})
	}
	return rows
}

func configKeyRows(keys []config.ConfigKeySpec) [][]string {
	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		env := strings.Join(key.Env, ", ")
		redacted := "no"
		if key.Redacted {
			redacted = "yes"
		}
		rows = append(rows, []string{key.Key, key.Type, key.Default, env, redacted, key.Description})
	}
	return rows
}

func configSettingsRows(fields []config.BillySettingsFieldSpec) [][]string {
	rows := make([][]string, 0, len(fields))
	for _, field := range fields {
		optional := "no"
		if field.Optional {
			optional = "yes"
		}
		rows = append(rows, []string{field.JSON, field.Field, field.Type, optional, field.Writer})
	}
	return rows
}

func configOutsideRows(items []configOutsideDoc) [][]string {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Item, item.Description})
	}
	return rows
}

func homeRelative(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	if base == "" || base == "." {
		return "$BILLYHARNESS_HOME"
	}
	return fmt.Sprintf("$BILLYHARNESS_HOME/%s", base)
}
