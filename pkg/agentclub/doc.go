// Package agentclub is a small public SDK for external agent-club adapters.
//
// It contains only the wire DTOs, request builders, HMAC signing helper, and
// HTTP client helpers needed by an adapter project to submit trusted events or
// trigger deliveries to a Billyharness gateway. It does not load Billyharness
// local config, read dotenv files, install adapters, schedule work, apply
// proposals, execute commands, or expose provider/model/tool override knobs.
package agentclub
