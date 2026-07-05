package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	TargetStatusOK         = "ok"
	TargetStatusStale      = "stale"
	TargetStatusMissing    = "missing"
	TargetStatusUnreadable = "unreadable"
)

type TargetStatus struct {
	Name         string
	Filename     string
	Status       string
	ExpectedHash string
	ActualHash   string
	Detail       string
}

var sourceHashRE = regexp.MustCompile(`(?m)<!-- source-hash: ([0-9a-f]{64}) -->`)

func VerifyAgainst(dir string) []TargetStatus {
	targets := Targets()
	out := make([]TargetStatus, 0, len(targets))
	for _, target := range targets {
		status := TargetStatus{Name: target.Name, Filename: target.Filename}
		expected, err := target.Fingerprint()
		if err != nil {
			status.Status = TargetStatusUnreadable
			status.Detail = "fingerprint: " + err.Error()
			out = append(out, status)
			continue
		}
		status.ExpectedHash = expected
		path := filepath.Join(dir, target.Filename)
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				status.Status = TargetStatusMissing
				status.Detail = "missing " + path
			} else {
				status.Status = TargetStatusUnreadable
				status.Detail = "read " + path + ": " + err.Error()
			}
			out = append(out, status)
			continue
		}
		actual, ok := parseSourceHash(body)
		if !ok {
			status.Status = TargetStatusUnreadable
			status.Detail = "source-hash footer missing in " + path
			out = append(out, status)
			continue
		}
		status.ActualHash = actual
		if actual != expected {
			status.Status = TargetStatusStale
			status.Detail = fmt.Sprintf("%s hash %s != live %s", path, actual, expected)
		} else {
			status.Status = TargetStatusOK
			status.Detail = path
		}
		out = append(out, status)
	}
	return out
}

func parseSourceHash(body []byte) (string, bool) {
	matches := sourceHashRE.FindSubmatch(body)
	if len(matches) != 2 {
		return "", false
	}
	return string(matches[1]), true
}

func fingerprintCLI() (string, error) {
	return sourceHash(cliReferenceInput())
}

func fingerprintCommands() (string, error) {
	return sourceHash(commandsReferenceInput())
}

func fingerprintConfig() (string, error) {
	return sourceHash(configReferenceInput())
}

func fingerprintEvents() (string, error) {
	return sourceHash(eventsReferenceInput())
}

func fingerprintGatewayAPI() (string, error) {
	return sourceHash(gatewayAPIReferenceInput())
}

func fingerprintPackages() (string, error) {
	data, err := packagesReferenceInput()
	if err != nil {
		return "", err
	}
	return sourceHash(data)
}

func fingerprintTools() (string, error) {
	return sourceHash(toolsReferenceInput())
}
