package config

import "strings"

const (
	AccessModeBuild   = "build"
	AccessModeGuarded = "guarded"
	AccessModePlan    = "plan"
)

func NormalizeAccessMode(value string) string {
	if mode, ok := ParseAccessMode(value); ok {
		return mode
	}
	return AccessModeBuild
}

func ParseAccessMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AccessModeGuarded, "safe":
		return AccessModeGuarded, true
	case AccessModePlan, "readonly", "read-only", "read_only", "analysis":
		return AccessModePlan, true
	case "", AccessModeBuild:
		return AccessModeBuild, true
	default:
		return "", false
	}
}

func AccessModeValues() []string {
	return []string{AccessModeBuild, AccessModeGuarded, AccessModePlan}
}
