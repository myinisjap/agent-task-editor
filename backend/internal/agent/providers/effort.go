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
//
// opencode: `opencode run --variant <value>` is the provider-specific
// reasoning-effort/variant selector. Unlike claude/codex there is no fixed
// vendor enum to validate or clamp against — accepted values are
// provider/model-specific (e.g. "minimal", "high", "max" depending on what
// the underlying model defines), opencode does not validate ours CLI-side,
// so an unsupported value may be silently ignored or rejected further
// upstream by the model provider. opencodeVariant therefore passes our own
// enum value straight through rather than guessing at a clamp.

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

// opencodeVariantLevels is the set of our own effort enum values that are
// passed through to opencode run's --variant flag unchanged. There is no
// fixed vendor enum here (see the package doc comment above): --variant is
// forwarded to whichever model provider opencode is configured against, and
// the set of variants that model actually understands isn't something this
// code can validate. Listing the five levels explicitly (mirroring
// claudeEffortLevels) still keeps an out-of-enum value from reaching the CLI.
var opencodeVariantLevels = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
}

// opencodeVariant maps our stored effort value to opencode run's --variant
// argument. ok is false when level is "" (unset — omit the flag entirely)
// or not a recognized value (defensive; validated at the API layer, so this
// should not normally happen). Unlike codexReasoningEffort, no clamping is
// applied: the value is passed through unchanged for every recognized
// level, since --variant's accepted values are model-specific and there is
// no vendor enum of our own to clamp against.
func opencodeVariant(level string) (string, bool) {
	if level == "" {
		return "", false
	}
	if !opencodeVariantLevels[level] {
		return "", false
	}
	return level, true
}
