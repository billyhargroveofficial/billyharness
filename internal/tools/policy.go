package tools

import (
	"errors"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
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
			Decision: "deny",
			Source:   "registry",
			Reason:   "unknown_tool",
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
		if !riskAllowedInPlanMode(risk) {
			decision.RequiresApproval = true
			decision.Decision = "deny"
			decision.Source = "access_mode"
			decision.Reason = "plan_mode_read_only"
			return decision
		}
	case config.AccessModeGuarded:
		if riskDeniedInGuardedMode(risk) {
			decision.RequiresApproval = true
			decision.Decision = "deny"
			decision.Source = "access_mode"
			decision.Reason = "guarded_mode_dangerous_tools_disabled"
			return decision
		}
	}
	switch {
	case riskRequiresDangerousApproval(risk):
		decision.RequiresApproval = true
		if r == nil || !r.toolPolicy.AutoApproveDangerous {
			decision.Decision = "deny"
			decision.Source = "config"
			decision.Reason = "dangerous_tools_disabled"
			return decision
		}
		decision.Source = "config"
		decision.Reason = "auto_approve_dangerous"
	case risk == protocol.RiskExternal:
		decision.RequiresApproval = true
		decision.Reason = "external_tool_allowed_by_existing_policy"
	}
	return decision
}

func riskAllowedInPlanMode(risk protocol.Risk) bool {
	switch protocol.RiskClass(risk) {
	case protocol.RiskLocalRead, protocol.RiskNetworkRead:
		return true
	default:
		return false
	}
}

func riskDeniedInGuardedMode(risk protocol.Risk) bool {
	switch protocol.RiskClass(risk) {
	case protocol.RiskLocalWrite, protocol.RiskNetworkWrite, protocol.RiskExecute, protocol.RiskExternalMutation, protocol.RiskSecretAccess:
		return true
	default:
		return false
	}
}

func riskRequiresDangerousApproval(risk protocol.Risk) bool {
	switch protocol.RiskClass(risk) {
	case protocol.RiskLocalWrite, protocol.RiskNetworkWrite, protocol.RiskExecute, protocol.RiskExternalMutation, protocol.RiskSecretAccess:
		return true
	default:
		return false
	}
}

func riskClass(risk protocol.Risk) protocol.Risk {
	return protocol.RiskClass(risk)
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
		if class := riskClass(d.Risk); class != "" && class != d.Risk {
			metadata["risk_class"] = class
		}
	}
	if d.AccessMode != "" {
		metadata["access_mode"] = d.AccessMode
	}
	return metadata
}

func DangerousToolDisabledMessage() string {
	return "tool disabled; set FAST_AGENT_AUTO_APPROVE_DANGEROUS=true or unset FAST_AGENT_AUTO_APPROVE_DANGEROUS to enable write/execute/side-effecting tools"
}

func PolicyDeniedMessage(decision PolicyDecision) string {
	switch decision.Reason {
	case "unknown_tool":
		if decision.Name == "" {
			return "unknown tool"
		}
		return "unknown tool " + decision.Name
	case "plan_mode_read_only":
		return "tool disabled in plan mode; switch access_mode out of plan to use write/execute/external tools"
	case "guarded_mode_dangerous_tools_disabled":
		return "tool disabled in guarded mode; switch access_mode=build to enable write/execute/side-effecting tools"
	case "mcp_side_effect_requires_allowlist":
		return "MCP tool disabled; side-effecting MCP tools require enabled_tools allowlisting in MCP config"
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

func (r *Registry) checkMCPTargetPolicy(tool Tool) (PolicyDecision, error) {
	decision := r.policyDecisionForRisk(tool.Spec.Name, tool.Spec.Risk)
	if decision.Allowed() && riskRequiresDangerousApproval(tool.Spec.Risk) && !tool.mcpPolicy.SideEffectAllowlisted {
		decision.RequiresApproval = true
		decision.Decision = "deny"
		decision.Source = "mcp_config"
		decision.Reason = "mcp_side_effect_requires_allowlist"
	}
	if decision.Allowed() {
		return decision, nil
	}
	return decision, errors.New(PolicyDeniedMessage(decision))
}

func addMCPTargetPolicyMetadata(metadata map[string]any, tool Tool, decision PolicyDecision) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	for key, value := range decision.Metadata() {
		metadata[key] = value
	}
	if class := riskClass(tool.Spec.Risk); class != "" {
		metadata["risk_class"] = class
	}
	if tool.mcpPolicy.ServerName != "" {
		metadata["mcp_server"] = tool.mcpPolicy.ServerName
	}
	if tool.mcpPolicy.OriginalName != "" {
		metadata["mcp_tool"] = tool.mcpPolicy.OriginalName
	}
	if tool.mcpPolicy.RiskSource != "" {
		metadata["mcp_risk_source"] = tool.mcpPolicy.RiskSource
	} else {
		metadata["mcp_risk_source"] = "unclassified_mcp_catalog"
	}
	metadataTrust := tool.mcpPolicy.MetadataTrust
	if metadataTrust == "" {
		metadataTrust = mcpclient.MCPMetadataTrustUntrusted
	}
	metadata["mcp_metadata_trust"] = metadataTrust
	metadata["mcp_description_trust"] = metadataTrust
	metadata["mcp_input_schema_trust"] = metadataTrust
	if riskRequiresDangerousApproval(tool.Spec.Risk) {
		metadata["mcp_side_effect_allowlisted"] = tool.mcpPolicy.SideEffectAllowlisted
	}
	return metadata
}
