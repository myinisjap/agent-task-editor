package runtime

import (
	"strings"
	"testing"
)

// TestMiseDataDir_RespectsEnvOverride verifies MiseDataDir prefers an
// explicit MISE_DATA_DIR env var (the operator override / the value
// backend/Dockerfile sets) over the $HOME-derived default.
func TestMiseDataDir_RespectsEnvOverride(t *testing.T) {
	t.Setenv("MISE_DATA_DIR", "/custom/mise-data")
	if got := MiseDataDir(); got != "/custom/mise-data" {
		t.Errorf("MiseDataDir() = %q, want %q", got, "/custom/mise-data")
	}
}

// TestMiseDataDir_FallsBackToHomeDefault verifies MiseDataDir falls back to
// $HOME/.local/share/mise (mise's own default) when MISE_DATA_DIR is unset —
// and, critically, never returns an empty string in that case (so callers
// never emit a bogus MISE_DATA_DIR= entry).
func TestMiseDataDir_FallsBackToHomeDefault(t *testing.T) {
	t.Setenv("MISE_DATA_DIR", "")
	t.Setenv("HOME", "/home/testuser")
	got := MiseDataDir()
	if got == "" {
		t.Fatal("MiseDataDir() returned empty string with a valid HOME set")
	}
	if !strings.HasSuffix(got, "/.local/share/mise") {
		t.Errorf("MiseDataDir() = %q, want it to end with /.local/share/mise", got)
	}
}

// TestUvCacheDir_RespectsEnvOverride mirrors TestMiseDataDir_RespectsEnvOverride
// for UV_CACHE_DIR — finding 6's fix: prep's uv venv and the agent run's own
// env must never silently diverge on which cache dir to use.
func TestUvCacheDir_RespectsEnvOverride(t *testing.T) {
	t.Setenv("UV_CACHE_DIR", "/custom/uv-cache")
	if got := UvCacheDir(); got != "/custom/uv-cache" {
		t.Errorf("UvCacheDir() = %q, want %q", got, "/custom/uv-cache")
	}
}

// TestUvCacheDir_FallsBackToHomeDefault verifies UvCacheDir falls back to
// $HOME/.cache/uv when UV_CACHE_DIR is unset, and never returns "" with a
// valid HOME — the empty-string case is reserved for an unresolvable HOME
// (os.UserHomeDir failure), and callers must skip emitting the env var
// entirely rather than set a bogus empty-valued UV_CACHE_DIR=.
func TestUvCacheDir_FallsBackToHomeDefault(t *testing.T) {
	t.Setenv("UV_CACHE_DIR", "")
	t.Setenv("HOME", "/home/testuser")
	got := UvCacheDir()
	if got == "" {
		t.Fatal("UvCacheDir() returned empty string with a valid HOME set")
	}
	if !strings.HasSuffix(got, "/.cache/uv") {
		t.Errorf("UvCacheDir() = %q, want it to end with /.cache/uv", got)
	}
}

func TestParsePins_Empty(t *testing.T) {
	pins, err := ParsePins("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pins != nil {
		t.Fatalf("expected nil pins for empty input, got %v", pins)
	}
}

func TestParsePins_Valid(t *testing.T) {
	pins, err := ParsePins(`[{"id":"go","version":"1.21"},{"id":"node","version":"22"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pins) != 2 {
		t.Fatalf("expected 2 pins, got %d", len(pins))
	}
	if pins[0] != (Pin{ID: "go", Version: "1.21"}) {
		t.Errorf("pin 0 = %+v", pins[0])
	}
	if pins[1] != (Pin{ID: "node", Version: "22"}) {
		t.Errorf("pin 1 = %+v", pins[1])
	}
}

func TestParsePins_AllAllowedLanguages(t *testing.T) {
	for _, lang := range []string{"go", "node", "python", "rust", "ruby", "java"} {
		if _, err := ParsePins(`[{"id":"` + lang + `","version":"1.0.0"}]`); err != nil {
			t.Errorf("language %q should be allowed: %v", lang, err)
		}
	}
}

func TestParsePins_RejectsUnknownLanguage(t *testing.T) {
	_, err := ParsePins(`[{"id":"php","version":"8.3"}]`)
	if err == nil {
		t.Fatal("expected error for unsupported language, got nil")
	}
}

func TestParsePins_RejectsAtInVersion(t *testing.T) {
	_, err := ParsePins(`[{"id":"go","version":"1.21@latest"}]`)
	if err == nil {
		t.Fatal("expected error for '@' in version, got nil")
	}
}

func TestParsePins_RejectsLeadingDash(t *testing.T) {
	_, err := ParsePins(`[{"id":"go","version":"-1.21"}]`)
	if err == nil {
		t.Fatal("expected error for leading dash in version, got nil")
	}
}

func TestParsePins_RejectsTooLongVersion(t *testing.T) {
	long := ""
	for i := 0; i < 33; i++ {
		long += "1"
	}
	_, err := ParsePins(`[{"id":"go","version":"` + long + `"}]`)
	if err == nil {
		t.Fatal("expected error for >32 char version, got nil")
	}
}

func TestParsePins_RejectsSpaceInVersion(t *testing.T) {
	_, err := ParsePins(`[{"id":"go","version":"1.21 rc1"}]`)
	if err == nil {
		t.Fatal("expected error for space in version, got nil")
	}
}

func TestParsePins_RejectsNonArrayJSON(t *testing.T) {
	_, err := ParsePins(`{"id":"go","version":"1.21"}`)
	if err == nil {
		t.Fatal("expected error for non-array JSON, got nil")
	}
}

func TestParsePins_RejectsInvalidJSON(t *testing.T) {
	_, err := ParsePins(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParsePins_RejectsEmptyVersion(t *testing.T) {
	_, err := ParsePins(`[{"id":"go","version":""}]`)
	if err == nil {
		t.Fatal("expected error for empty version, got nil")
	}
}

// TestParsePins_RejectsDuplicateLanguage verifies two pins for the same
// language id are rejected — ambiguous config (which version wins?) that
// every writer of runtime_languages routes through ParsePins, so this is the
// single enforcement point (the frontend form separately blocks it
// client-side, but a direct API write must still be caught here).
func TestParsePins_RejectsDuplicateLanguage(t *testing.T) {
	_, err := ParsePins(`[{"id":"go","version":"1.21"},{"id":"go","version":"1.22"}]`)
	if err == nil {
		t.Fatal("expected error for duplicate language id, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %v, want it to mention 'duplicate'", err)
	}
}

// TestParsePins_AllowsMultipleDistinctLanguages is the non-regression
// counterpart to the duplicate check above: distinct language ids are fine.
func TestParsePins_AllowsMultipleDistinctLanguages(t *testing.T) {
	pins, err := ParsePins(`[{"id":"go","version":"1.21"},{"id":"node","version":"22"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pins) != 2 {
		t.Fatalf("expected 2 pins, got %d", len(pins))
	}
}
