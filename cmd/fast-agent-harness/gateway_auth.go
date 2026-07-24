package main

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayauth"
)

type gatewayServeAuth struct {
	Token         string
	GeneratedPath string
}

func resolveGatewayServeAuth(explicit string, authRequired, devAllowLoopbackNoAuth bool) (gatewayServeAuth, error) {
	if strings.TrimSpace(explicit) != "" {
		token, err := gatewayauth.ValidateToken(explicit)
		if err != nil {
			return gatewayServeAuth{}, fmt.Errorf("invalid -auth-token: %w", err)
		}
		return gatewayServeAuth{Token: token}, nil
	}

	resolved, err := gatewayauth.Resolve()
	if err != nil {
		return gatewayServeAuth{}, fmt.Errorf("resolve gateway bearer token: %w", err)
	}
	if strings.TrimSpace(resolved.Value) == "" {
		if authRequired {
			return gatewayServeAuth{}, fmt.Errorf("gateway auth token required for a non-loopback listen address; preprovision %s or set %s and protect bearer traffic with HTTPS or an SSH tunnel", gatewayauth.DefaultPath(), gatewayauth.PrimaryEnv)
		}
		if devAllowLoopbackNoAuth {
			return gatewayServeAuth{}, nil
		}
	}

	ensured, err := gatewayauth.Ensure()
	if err != nil {
		return gatewayServeAuth{}, fmt.Errorf("provision gateway bearer token: %w", err)
	}
	token := strings.TrimSpace(ensured.Value)
	if token == "" {
		return gatewayServeAuth{}, fmt.Errorf("gateway bearer token resolution returned an empty credential")
	}
	result := gatewayServeAuth{Token: token}
	if ensured.Created {
		result.GeneratedPath = ensured.Path
	}
	return result, nil
}
