package providers

// Reasoning effort level mapping.
//
// AgentConfig.Effort is the provider-agnostic value stored on the agent
// config: "" (unset/provider default), "low", "medium", "high", "xhigh", or
// "max". Each provider that supports a tunable reasoning effort translates
// it to its own CLI surface. Providers with no such concept ignore the
// field entirely.
//
// claude (verified against a live claude CLI, v2.1.223): first-class
// `--effort <level>` flag accepting exactly low|medium|high|xhigh|max (no
// "off" — there is no way to force minimal/no reasoning via this flag). An
// unrecognized value does NOT make the CLI exit non-zero — it prints
// "Warning: Unknown --effort value '<v>' — ignoring it and using the
// default effort." to stderr and silently continues at the provider
// default. That makes backend-side validation (see agents.go) the only
// thing standing between an operator typo/drift and a silently-degraded
// run — the CLI will not catch it for us. Effort can also be a no-op for
// reasons entirely outside our control: not every model supports effort
// levels, and an organization's Anthropic account can restrict which
// levels are available; both fail silently (no error surfaced to this
// process) rather than rejecting the run.
//
// codex_cli: no --effort flag; reasoning effort is set via a `-c
// model_reasoning_effort="<level>"` config override, accepting exactly
// minimal|low|medium|high. Since our own enum has no "off"/"minimal" tier,
// xhigh/max (which codex has no equivalent of) clamp down to "high" rather
// than being passed through unrecognized.

// claudeEffortLevels is the exact set of values accepted by claude's
// --effort flag (verified against `claude --help`, v2.1.223).
var claudeEffortLevels = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
}

// claudeEffort maps our stored effort value to the argument for claude's
// --effort flag. ok is false when level is "" (unset — omit the flag
// entirely) or not a recognized value (defensive; validated at the API
// layer, so this should not normally happen).
func claudeEffort(level string) (string, bool) {
	if level == "" {
		return "", false
	}
	if !claudeEffortLevels[level] {
		return "", false
	}
	return level, true
}

// codexReasoningEffort maps our stored effort value to codex's
// model_reasoning_effort config value. codex only accepts
// minimal|low|medium|high; xhigh/max (levels codex has no equivalent tier
// for) clamp down to "high" rather than passing an unrecognized value
// through. ok is false when level is "" (unset — omit the override).
func codexReasoningEffort(level string) (string, bool) {
	switch level {
	case "":
		return "", false
	case "low", "medium", "high":
		return level, true
	case "xhigh", "max":
		return "high", true
	default:
		return "", false
	}
}
