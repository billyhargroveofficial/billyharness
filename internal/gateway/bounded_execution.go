package gateway

import (
	"fmt"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	boundedAutomationMaxToolCalls = 12
	boundedIsolatedMaxToolCalls   = 4
)

func validateRunExecutionLimits(req RunRequest) error {
	_, err := executionContractForRunRequest(req)
	return err
}

func executionContractForRunRequest(req RunRequest) (*protocol.ExecutionContractAttestation, error) {
	accessMode := req.AccessMode
	expectedMaxToolCalls := 0
	switch accessMode {
	case gatewayapi.AccessModeBoundedAutomationV1:
		expectedMaxToolCalls = boundedAutomationMaxToolCalls
	case gatewayapi.AccessModeBoundedIsolatedPlanV1:
		expectedMaxToolCalls = boundedIsolatedMaxToolCalls
	case gatewayapi.AccessModeIsolatedPlanV1:
		if req.MaxToolCalls != nil && *req.MaxToolCalls <= 0 {
			return nil, fmt.Errorf("max_tool_calls must be a positive integer")
		}
		return nil, nil
	default:
		if accessMode != "" {
			if _, ok := config.ParseAccessMode(accessMode); !ok {
				return nil, fmt.Errorf("unsupported access_mode %q", req.AccessMode)
			}
		}
		if req.MaxToolCalls != nil && *req.MaxToolCalls <= 0 {
			return nil, fmt.Errorf("max_tool_calls must be a positive integer")
		}
		return nil, nil
	}

	if req.MaxToolCalls == nil {
		return nil, fmt.Errorf("max_tool_calls is required for access_mode %q", accessMode)
	}
	if *req.MaxToolCalls != expectedMaxToolCalls {
		return nil, fmt.Errorf(
			"max_tool_calls must equal %d for access_mode %q",
			expectedMaxToolCalls,
			accessMode,
		)
	}
	return &protocol.ExecutionContractAttestation{
		ExecutionContract:       accessMode,
		ProviderMaxRetries:      0,
		ProviderFailoverEnabled: false,
		MaxToolCalls:            expectedMaxToolCalls,
	}, nil
}

func effectiveRunAccessMode(req RunRequest) string {
	switch req.AccessMode {
	case gatewayapi.AccessModeBoundedAutomationV1:
		return ""
	case gatewayapi.AccessModeIsolatedPlanV1, gatewayapi.AccessModeBoundedIsolatedPlanV1:
		return config.AccessModePlan
	default:
		return req.AccessMode
	}
}

func applyRunExecutionLimits(settings *config.RuntimeDiffSettings, req RunRequest, contract *protocol.ExecutionContractAttestation) {
	if settings == nil {
		return
	}
	if req.MaxToolCalls != nil {
		settings.Runtime.MaxToolCalls = *req.MaxToolCalls
	}
	if contract == nil {
		return
	}

	// Bounded automation must have one provider attempt per model turn and no
	// hidden helper-model calls. The provider stack has no model failover path;
	// the attestation records that effective invariant as false.
	settings.Runtime.ProviderMaxRetries = 0
	settings.Provider.Limits.ProviderMaxRetries = 0
	settings.Runtime.ContextCompactStrategy = "deterministic"
	settings.ToolPolicy.WebSummaryMode = "extractive"
}
