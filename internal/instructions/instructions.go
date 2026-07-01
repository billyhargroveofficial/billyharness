// Portions of this file are adapted from OpenAI Codex.
// Original project: https://github.com/openai/codex
// Copyright 2025 OpenAI
// Licensed under the Apache License, Version 2.0.

package instructions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	defaultAgentsFilename = "AGENTS.md"
	localAgentsFilename   = "AGENTS.override.md"
	projectDocSeparator   = "\n\n--- project-doc ---\n\n"
	contextStartMarker    = "# AGENTS.md instructions"
	contextOpenMarker     = "<INSTRUCTIONS>"
	contextEndMarker      = "</INSTRUCTIONS>"
	profileStartMarker    = "# Billyharness profile"
	profileOpenMarker     = "<SOUL>"
	profileEndMarker      = "</SOUL>"
)

type Source struct {
	Path    string `json:"path"`
	Scope   string `json:"scope"`
	Bytes   int    `json:"bytes,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Capped  bool   `json:"capped,omitempty"`
	Skipped string `json:"skipped,omitempty"`
}

type Loaded struct {
	Text      string
	Directory string
	Sources   []Source
}

type Profile struct {
	Name string
	Path string
	Text string
}

func Load(settings config.InstructionSettings) Loaded {
	var parts []instructionPart
	if text, source := loadGlobalInstructions(); text != "" {
		parts = append(parts, instructionPart{text: text, source: source})
	}
	project := loadProjectInstructions(settings)
	parts = append(parts, project.parts...)
	return Loaded{
		Text:      joinParts(parts),
		Directory: project.directory,
		Sources:   sources(parts),
	}
}

func Message(settings config.InstructionSettings) (protocol.Message, bool) {
	loaded := Load(settings)
	if strings.TrimSpace(loaded.Text) == "" {
		return protocol.Message{}, false
	}
	return protocol.Message{
		Role:    protocol.RoleUser,
		Content: loaded.ContextualText(),
	}, true
}

func ProfileMessage(settings config.InstructionSettings) (protocol.Message, bool) {
	profile, ok := LoadProfile(settings)
	if !ok {
		return protocol.Message{}, false
	}
	return protocol.Message{
		Role:    protocol.RoleSystem,
		Content: profile.ContextualText(),
	}, true
}

func LoadProfile(settings config.InstructionSettings) (Profile, bool) {
	if strings.TrimSpace(settings.Profile.Profile) == "" {
		return Profile{}, false
	}
	name := config.NormalizeProfileName(settings.Profile.Profile)
	path, err := config.EnsureDefaultProfileFile(name)
	if err != nil || strings.TrimSpace(path) == "" {
		return Profile{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, false
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return Profile{}, false
	}
	return Profile{Name: name, Path: path, Text: text}, true
}

func (l Loaded) ContextualText() string {
	directory := ""
	if l.Directory != "" {
		directory = " for " + l.Directory
	}
	return contextStartMarker + directory + "\n\n" +
		contextOpenMarker + "\n" +
		l.Text + "\n" +
		contextEndMarker
}

func (p Profile) ContextualText() string {
	return profileStartMarker + ": " + p.Name + "\n" +
		"Source: " + p.Path + "\n\n" +
		profileOpenMarker + "\n" +
		p.Text + "\n" +
		profileEndMarker
}

type instructionPart struct {
	text    string
	project bool
	source  Source
}

type projectLoad struct {
	parts     []instructionPart
	directory string
}

func loadGlobalInstructions() (string, Source) {
	for _, dir := range globalDirs() {
		for _, name := range baseCandidates(nil) {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := strings.TrimSpace(string(raw))
			if text != "" {
				return text, instructionSource(path, "global", raw, false)
			}
		}
	}
	return "", Source{}
}

func loadProjectInstructions(settings config.InstructionSettings) projectLoad {
	cwd := instructionCWD(settings)
	if cwd == "" || settings.ProjectDocMaxBytes == 0 {
		return projectLoad{}
	}
	root := projectRoot(cwd)
	dirs := searchDirs(root, cwd)
	candidates := baseCandidates(settings.ProjectDocFallbacks)
	remaining := settings.ProjectDocMaxBytes
	var parts []instructionPart
	for _, dir := range dirs {
		if remaining <= 0 {
			break
		}
		for _, name := range candidates {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			capped := false
			if len(raw) > remaining {
				raw = raw[:remaining]
				capped = true
			}
			if strings.TrimSpace(string(raw)) != "" {
				parts = append(parts, instructionPart{
					text:    string(raw),
					project: true,
					source:  instructionSource(path, "project", raw, capped),
				})
				remaining -= len(raw)
			}
			break
		}
	}
	return projectLoad{parts: parts, directory: cwd}
}

func instructionSource(path, scope string, raw []byte, capped bool) Source {
	sum := sha256.Sum256(raw)
	return Source{
		Path:   path,
		Scope:  scope,
		Bytes:  len(raw),
		SHA256: hex.EncodeToString(sum[:]),
		Capped: capped,
	}
}

func instructionCWD(settings config.InstructionSettings) string {
	for _, root := range settings.WorkspaceRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			return filepath.Clean(abs)
		}
		return filepath.Clean(root)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cwd)
}

func projectRoot(cwd string) string {
	cursor := filepath.Clean(cwd)
	for {
		if exists(filepath.Join(cursor, ".git")) {
			return cursor
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return ""
		}
		cursor = parent
	}
}

func searchDirs(root, cwd string) []string {
	cwd = filepath.Clean(cwd)
	if root == "" {
		return []string{cwd}
	}
	root = filepath.Clean(root)
	var dirs []string
	for cursor := cwd; ; cursor = filepath.Dir(cursor) {
		dirs = append(dirs, cursor)
		if cursor == root || filepath.Dir(cursor) == cursor {
			break
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

func baseCandidates(fallbacks []string) []string {
	names := []string{localAgentsFilename, defaultAgentsFilename}
	seen := map[string]bool{localAgentsFilename: true, defaultAgentsFilename: true}
	for _, fallback := range fallbacks {
		fallback = strings.TrimSpace(fallback)
		if fallback == "" || seen[fallback] {
			continue
		}
		seen[fallback] = true
		names = append(names, fallback)
	}
	return names
}

func joinParts(parts []instructionPart) string {
	var b strings.Builder
	var hasPrevious bool
	var previousWasProject bool
	for _, part := range parts {
		text := strings.TrimSpace(part.text)
		if text == "" {
			continue
		}
		if hasPrevious {
			if part.project && !previousWasProject {
				b.WriteString(projectDocSeparator)
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(text)
		hasPrevious = true
		previousWasProject = part.project
	}
	return b.String()
}

func sources(parts []instructionPart) []Source {
	out := make([]Source, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part.text) != "" {
			out = append(out, part.source)
		}
	}
	return out
}

func globalDirs() []string {
	var dirs []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = filepath.Clean(path)
		for _, existing := range dirs {
			if existing == path {
				return
			}
		}
		dirs = append(dirs, path)
	}
	if explicit := os.Getenv("BILLYHARNESS_HOME"); explicit != "" {
		add(explicit)
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, "billyharness"))
	}
	if explicit := os.Getenv("CODEX_HOME"); explicit != "" {
		add(explicit)
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".codex"))
	}
	return dirs
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
