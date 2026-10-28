package autonomy

import (
	"reflect"
	"strings"
	"testing"
)

var known = []string{"migration", "ci_config"}

func TestValidate(t *testing.T) {
	c := Config{Mode: ModeOnFlags, AskOn: []string{"ci_config", "migration", "ci_config"}, HoldSeconds: 5}
	if err := c.Validate(known); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.AskOn, []string{"ci_config", "migration"}) {
		t.Fatalf("dedupe: %v", c.AskOn)
	}
	neg := -1.0
	bad := []Config{
		{Mode: "bogus"},
		{Mode: ModeAuto, AskOn: []string{"nope"}},
		{Mode: ModeAuto, HoldSeconds: -1},
		{Mode: ModeAuto, MaxCostUSD: &neg},
	}
	for i, b := range bad {
		if err := b.Validate(known); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	e := Config{Mode: ModeAuto, AskOn: []string{"nope"}}
	if err := e.Validate(known); !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "migration") {
		t.Fatalf("err = %v", err)
	}
}

func TestHold(t *testing.T) {
	for in, want := range map[string]int{"10m": 600, "1h": 3600, "90s": 90, "0s": 0} {
		got, err := ParseHold(in)
		if err != nil || got != want {
			t.Fatalf("%s: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"-1m", "1.5s", "abc", ""} {
		if _, err := ParseHold(in); err == nil {
			t.Fatalf("%q: expected error", in)
		}
	}
	for sec, want := range map[int]string{600: "10m", 3600: "1h", 90: "90s", 0: "0s", 7200: "2h"} {
		if got := FormatHold(sec); got != want {
			t.Fatalf("%d: %s want %s", sec, got, want)
		}
	}
}
