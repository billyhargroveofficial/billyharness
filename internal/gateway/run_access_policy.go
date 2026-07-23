package gateway

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

const GatewayAllowedRunAccessModeEnv = "BILLYHARNESS_GATEWAY_ALLOWED_RUN_ACCESS_MODE"

func ParseAllowedRunAccessMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "", gatewayapi.AccessModeBoundedIsolatedPlanV1:
		return value, nil
	default:
		return "", fmt.Errorf(
			"%s must be empty or %q, got %q",
			GatewayAllowedRunAccessModeEnv,
			gatewayapi.AccessModeBoundedIsolatedPlanV1,
			value,
		)
	}
}

func (s *Server) validateRunAccessPolicy(req RunRequest) error {
	if s == nil {
		return nil
	}
	if s.runAccessPolicyErr != nil {
		return fmt.Errorf("gateway run access policy is invalid")
	}
	if s.allowedRunAccessMode == "" {
		return nil
	}
	if req.AccessMode != s.allowedRunAccessMode {
		return fmt.Errorf(
			"gateway allows only access_mode %q",
			s.allowedRunAccessMode,
		)
	}
	return nil
}

func (s *Server) publicAllowedRunAccessMode() string {
	if s == nil || s.runAccessPolicyErr != nil {
		return ""
	}
	return s.allowedRunAccessMode
}
