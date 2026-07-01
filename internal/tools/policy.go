package tools

import (
	"errors"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type PolicyDecision struct {
	Name             string
	Risk             protocol.Risk
	KnownRisk        bool
	RequiresApproval bool
	Decision         string
	Source           string
	Reason           string
	AccessMode       string
}

func (r *Registry) PolicyDecision(name string) PolicyDecision {
	if r == nil {
		return PolicyDecision{
			Name:     name,
			Decision: "allow",
			Source:   "auto",
			Reason:   "tool_registry_unavailable",
		}
	}
	risk, ok := r.Risk(name)
	if !ok {
		return PolicyDecision{
			Name:     name,
			Decision: "allow",
			Source:   "auto",
			Reason:   "unknown_tool_checked_at_execution",
		}
	}
	return r.policyDecisionForRisk(name, risk)
}

func (r *Registry) policyDecisionForRisk(name string, risk protocol.Risk) PolicyDecision {
	accessMode := config.AccessModeBuild
	if r != nil {
		accessMode = config.NormalizeAccessMode(r.toolPolicy.AccessMode)
	}
	decision := PolicyDecision{
		Name:       name,
		Risk:       risk,
		KnownRisk:  true,
		Decision:   "allow",
		Source:     "auto",
		Reason:     "safe_tool",
		AccessMode: accessMode,
	}
	switch decision.AccessMode {
	case config.AccessModePlan:
		switch risk {
		case protocol.RiskWrite, protocol.RiskExecute, protocol.RiskExternal:
			decision.RequiresApproval = true
			decision.Decision = "deny"
			decision.Source = "access_mode"
			decision.Reason = "plan_mode_read_only"
			return decision
		}
	case config.AccessModeGuarded:
		switch risk {
		case protocol.RiskWrite, protocol.RiskExecute:
			decision.RequiresApproval = true
			decision.Decision = "deny"
			decision.Source = "access_mode"
			decision.Reason = "guarded_mode_dangerous_tools_disabled"
			return decision
		}
	}
	switch risk {
	case protocol.RiskWrite, protocol.RiskExecute:
		decision.RequiresApproval = true
		if r == nil || !r.toolPolicy.AutoApproveDangerous {
			decision.Decision = "deny"
			decision.Source = "config"
			decision.Reason = "dangerous_tools_disabled"
			return decision
		}
		decision.Source = "config"
		decision.Reason = "auto_approve_dangerous"
	case protocol.RiskExternal:
		decision.RequiresApproval = true
		decision.Reason = "external_tool_allowed_by_existing_policy"
	}
	return decision
}

func (d PolicyDecision) Allowed() bool {
	return d.Decision != "deny"
}

func (d PolicyDecision) Metadata() map[string]any {
	metadata := map[string]any{
		"permission_decision": d.Decision,
		"permission_source":   d.Source,
		"permission_reason":   d.Reason,
	}
	if d.KnownRisk {
		metadata["risk"] = d.Risk
	}
	if d.AccessMode != "" {
		metadata["access_mode"] = d.AccessMode
	}
	return metadata
}

func DangerousToolDisabledMessage() string {
	return "tool disabled; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=true or unset FAST_AGENT_AUTO_APPROVE_DANGEROUS to enable write/execute tools"
}

func PolicyDeniedMessage(decision PolicyDecision) string {
	switch decision.Reason {
	case "plan_mode_read_only":
		return "tool disabled in plan mode; switch access_mode out of plan to use write/execute/external tools"
	case "guarded_mode_dangerous_tools_disabled":
		return "tool disabled in guarded mode; switch access_mode=build to enable write/execute tools"
	default:
		return DangerousToolDisabledMessage()
	}
}

func (r *Registry) checkPolicy(tool Tool) (PolicyDecision, error) {
	decision := r.policyDecisionForRisk(tool.Spec.Name, tool.Spec.Risk)
	if decision.Allowed() {
		return decision, nil
	}
	return decision, errors.New(PolicyDeniedMessage(decision))
}
