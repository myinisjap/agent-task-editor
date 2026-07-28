package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeEnv_BlocksDangerousKeys verifies mergeEnv drops every key in
// dangerousEnvKeys (case-insensitively), regardless of how the caller cased
// it, while preserving benign keys unchanged. This is an untested security
// control (see #251 §4): user-supplied AgentConfig.Env values must never be
// able to hijack subprocess execution via PATH/LD_PRELOAD/etc.
func TestMergeEnv_BlocksDangerousKeys(t *testing.T) {
	base := []string{"EXISTING=1"}

	for key := range dangerousEnvKeys {
		t.Run(key, func(t *testing.T) {
			extra := map[string]string{key: "evil-value"}
			out := mergeEnv(base, extra)
			for _, kv := range out {
				if strings.HasPrefix(kv, key+"=") {
					t.Fatalf("mergeEnv did not block dangerous key %q: got %v", key, out)
				}
			}
		})
	}
}

// TestMergeEnv_BlocksDangerousKeys_CaseInsensitive verifies the block applies
// regardless of the case the caller used for the key (mergeEnv upper-cases
// before checking dangerousEnvKeys).
func TestMergeEnv_BlocksDangerousKeys_CaseInsensitive(t *testing.T) {
	out := mergeEnv(nil, map[string]string{"path": "/evil/bin", "Ld_Preload": "/evil.so"})
	for _, kv := range out {
		lower := strings.ToLower(kv)
		if strings.HasPrefix(lower, "path=") || strings.HasPrefix(lower, "ld_preload=") {
			t.Fatalf("mergeEnv did not block a differently-cased dangerous key: got %v", out)
		}
	}
}

// TestMergeEnv_PreservesBenignKeys verifies non-dangerous keys pass through
// unmodified, appended after the base slice.
func TestMergeEnv_PreservesBenignKeys(t *testing.T) {
	base := []string{"EXISTING=1"}
	out := mergeEnv(base, map[string]string{"MY_CUSTOM_VAR": "hello", "ANOTHER_ONE": "world"})

	want := map[string]bool{"EXISTING=1": false, "MY_CUSTOM_VAR=hello": false, "ANOTHER_ONE=world": false}
	for _, kv := range out {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
	}
	for kv, found := range want {
		if !found {
			t.Errorf("expected %q in merged env, got %v", kv, out)
		}
	}
	if len(out) != 3 {
		t.Errorf("len(out) = %d, want 3 (base + 2 benign extras), got %v", len(out), out)
	}
}

// TestMergeEnv_DoesNotMutateBase verifies mergeEnv doesn't alias/mutate the
// caller's base slice in place (base is os.Environ() in production — mutating
// it in place would be a subtle cross-call bug).
func TestMergeEnv_DoesNotMutateBase(t *testing.T) {
	base := []string{"A=1", "B=2"}
	baseCopy := append([]string(nil), base...)

	_ = mergeEnv(base, map[string]string{"C": "3"})

	for i := range base {
		if base[i] != baseCopy[i] {
			t.Errorf("mergeEnv mutated base in place: got %v, want %v", base, baseCopy)
		}
	}
}

// TestMergeEnv_EmptyExtra verifies a nil/empty extra map returns a copy of
// base unchanged.
func TestMergeEnv_EmptyExtra(t *testing.T) {
	base := []string{"A=1"}
	out := mergeEnv(base, nil)
	if len(out) != 1 || out[0] != "A=1" {
		t.Errorf("mergeEnv(base, nil) = %v, want %v", out, base)
	}
}

// --- openRawDump / rawDump (cli.go) ---

// TestOpenRawDump_NoopWhenEnvUnset verifies openRawDump returns nil (a
// no-op dump) when AGENT_RAW_LOG_DIR is not set, and that WriteLine/Close on
// a nil *rawDump don't panic — the hot path for every production run, since
// this env var is dev-only.
func TestOpenRawDump_NoopWhenEnvUnset(t *testing.T) {
	t.Setenv("AGENT_RAW_LOG_DIR", "")
	d := openRawDump("run-1")
	if d != nil {
		t.Fatalf("want nil rawDump when AGENT_RAW_LOG_DIR is unset, got %v", d)
	}
	// Must not panic on a nil receiver.
	d.WriteLine("hello")
	d.Close()
}

// TestOpenRawDump_WritesToConfiguredDir verifies that when AGENT_RAW_LOG_DIR
// is set, openRawDump creates <dir>/<runID>.jsonl and WriteLine appends
// newline-terminated lines to it.
func TestOpenRawDump_WritesToConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_RAW_LOG_DIR", dir)

	d := openRawDump("run-xyz")
	if d == nil {
		t.Fatal("want non-nil rawDump when AGENT_RAW_LOG_DIR is set")
	}
	d.WriteLine(`{"type":"result"}`)
	d.WriteLine(`{"type":"done"}`)
	d.Close()

	data, err := os.ReadFile(filepath.Join(dir, "run-xyz.jsonl"))
	if err != nil {
		t.Fatalf("expected raw dump file to exist: %v", err)
	}
	want := "{\"type\":\"result\"}\n{\"type\":\"done\"}\n"
	if string(data) != want {
		t.Errorf("raw dump content = %q, want %q", string(data), want)
	}
}
