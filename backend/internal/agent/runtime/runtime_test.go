package runtime

import "testing"

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
