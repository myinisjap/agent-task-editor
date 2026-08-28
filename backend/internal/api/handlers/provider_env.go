package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// This file implements write-only semantics for provider_configs.env.
//
// env stores a JSON object of secrets (API keys and similar) that get merged
// into the provider CLI's environment at dispatch time (see
// agent/dispatcher.go and agent/terminal.go, which read the column directly
// from the DB and are NOT touched by any of this). Every HTTP read path,
// however, must never echo those values back to a client: an operator with
// read-only access to the API (or anyone with browser devtools open) would
// otherwise be handed every configured secret in cleartext.
//
// The contract, matching the frontend's existing (previously dead) handling
// of the "***" sentinel in ProviderConfigForm.tsx / ProviderConfigPage.tsx:
//
//   - Reads: every env value is replaced with EnvRedactedValue ("***"). Key
//     names are left as-is (useful for the UI to show what's configured).
//     A genuinely empty value ("") is left empty, since it isn't a secret and
//     round-tripping "***" for it would be misleading.
//   - Writes: a value of EnvRedactedValue means "keep whatever is already
//     stored for this key" (so a client that GETs a redacted config and PUTs
//     the whole thing back doesn't wipe secrets it never saw). Any other
//     value (including "") overwrites. A key present in the existing stored
//     env but omitted from the incoming object is deleted.
//
// This is response redaction only, not encryption: values are still stored
// in cleartext in SQLite, and GET /api/v1/backup (a raw DB dump) still
// contains them by design. See docs/agents.md "Environment Variable
// Security".

// EnvRedactedValue is the sentinel returned in place of a provider-config env
// value on every read path. Values never leave the server; only key names do.
const EnvRedactedValue = "***"

// redactEnvJSON takes a provider_configs.env JSON-object-as-string and
// returns an equivalent JSON object with every value replaced by
// EnvRedactedValue (except genuinely empty values, which stay empty).
//
// It fails closed: an empty input, or input that isn't a JSON object of
// string values (malformed or legacy data), becomes "{}" rather than being
// passed through — we never want to risk leaking a raw value we didn't
// recognize the shape of.
func redactEnvJSON(env string) string {
	if env == "" {
		return "{}"
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(env), &m); err != nil {
		return "{}"
	}
	redacted := make(map[string]string, len(m))
	for k, v := range m {
		if v == "" {
			redacted[k] = ""
			continue
		}
		redacted[k] = EnvRedactedValue
	}
	out, err := json.Marshal(redacted)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// mergeEnvJSON resolves an incoming (client-supplied) env JSON object against
// the existing stored one, per the write contract documented above:
//
//   - The result's key set is exactly the incoming key set (a key omitted
//     from incoming is deleted).
//   - For each incoming key: if its value is EnvRedactedValue, the existing
//     value for that key is kept (or the key is dropped entirely if it
//     wasn't in existing — we never want to persist the literal sentinel);
//     otherwise the incoming value is used verbatim (including "").
//
// incoming must parse as a JSON object of string values, or an error is
// returned so the caller can respond 400. existing is treated as an empty
// map if it fails to parse (defensive; existing rows are expected to always
// be valid).
func mergeEnvJSON(incoming, existing string) (string, error) {
	var incomingMap map[string]string
	if err := json.Unmarshal([]byte(incoming), &incomingMap); err != nil {
		return "", fmt.Errorf("env must be a JSON object of string values")
	}
	var existingMap map[string]string
	_ = json.Unmarshal([]byte(existing), &existingMap) // best-effort; nil map is fine

	merged := make(map[string]string, len(incomingMap))
	for k, v := range incomingMap {
		if v == EnvRedactedValue {
			if ev, ok := existingMap[k]; ok {
				merged[k] = ev
			}
			continue
		}
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// redactedProviderConfig returns a copy of pc with its env values masked.
// gen.ProviderConfig is a plain struct, so this copies by value and never
// mutates the caller's pc.
func redactedProviderConfig(pc gen.ProviderConfig) gen.ProviderConfig {
	pc.Env = redactEnvJSON(pc.Env)
	return pc
}
