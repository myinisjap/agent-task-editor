package autonomy

import (
	"reflect"
	"testing"
)

func f(v float64) *float64 { return &v }

func codes(d Decision) []string {
	out := []string{}
	for _, r := range d.Reasons {
		out = append(out, r.Code)
	}
	return out
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want string
		code []string
	}{
		{"ask mode", Input{Mode: ModeAsk, HasDiff: true}, "ask", []string{ReasonModeAsk}},
		{"empty mode", Input{}, "ask", []string{ReasonModeAsk}},
		{"unknown mode", Input{Mode: "x"}, "ask", []string{ReasonModeAsk}},
		{"auto clean", Input{Mode: ModeAuto}, "approve", []string{ReasonAutoPolicy}},
		{"auto no diff approves", Input{Mode: ModeAuto, HasDiff: false}, "approve", []string{ReasonAutoPolicy}},
		{"auto ignores flags", Input{Mode: ModeAuto, Flags: []string{"migration"}, AskOn: []string{"migration"}}, "approve", []string{ReasonAutoPolicy}},
		{"on_flags clean", Input{Mode: ModeOnFlags, HasDiff: true}, "approve", []string{ReasonNoBlockingFlags}},
		{"on_flags no diff", Input{Mode: ModeOnFlags}, "ask", []string{ReasonNoDiff}},
		{"on_flags flag not in ask_on", Input{Mode: ModeOnFlags, HasDiff: true, Flags: []string{"ci_config"}, AskOn: []string{"migration"}}, "approve", []string{ReasonNoBlockingFlags}},
		{"on_flags flag in ask_on", Input{Mode: ModeOnFlags, HasDiff: true, Flags: []string{"ci_config", "migration"}, AskOn: []string{"migration", "ci_config"}}, "ask", []string{ReasonFlagRaised, ReasonFlagRaised}},
		{"on_flags empty ask_on", Input{Mode: ModeOnFlags, HasDiff: true, Flags: []string{"migration"}}, "approve", []string{ReasonNoBlockingFlags}},
		{"always_ask overrides auto", Input{Mode: ModeAuto, AlwaysAsk: true}, "ask", []string{ReasonAlwaysAsk}},
		{"verify pass", Input{Mode: ModeAuto, VerifyStatus: "pass"}, "approve", []string{ReasonAutoPolicy}},
		{"verify fail", Input{Mode: ModeAuto, VerifyStatus: "fail"}, "ask", []string{ReasonVerifyFailed}},
		{"verify timeout", Input{Mode: ModeAuto, VerifyStatus: "timeout"}, "ask", []string{ReasonVerifyFailed}},
		{"verify error", Input{Mode: ModeAuto, VerifyStatus: "error"}, "ask", []string{ReasonVerifyFailed}},
		{"cost equal", Input{Mode: ModeAuto, CostUSD: 5, MaxCostUSD: f(5)}, "approve", []string{ReasonAutoPolicy}},
		{"cost above", Input{Mode: ModeAuto, CostUSD: 6.2, MaxCostUSD: f(5)}, "ask", []string{ReasonCostExceeded}},
		{"cost no limit", Input{Mode: ModeAuto, CostUSD: 600}, "approve", []string{ReasonAutoPolicy}},
		{"all combined", Input{Mode: ModeOnFlags, AlwaysAsk: true, VerifyStatus: "fail", CostUSD: 9, MaxCostUSD: f(1)}, "ask",
			[]string{ReasonAlwaysAsk, ReasonVerifyFailed, ReasonCostExceeded, ReasonNoDiff}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Evaluate(tc.in)
			if d.Decision != tc.want {
				t.Fatalf("decision = %s, want %s (%v)", d.Decision, tc.want, d.Reasons)
			}
			if !reflect.DeepEqual(codes(d), tc.code) {
				t.Fatalf("codes = %v, want %v", codes(d), tc.code)
			}
			if d.Reasons == nil {
				t.Fatal("nil reasons")
			}
		})
	}
	if d := Evaluate(Input{Mode: ModeAuto, CostUSD: 6.2, MaxCostUSD: f(5)}); d.Reasons[0].Detail != "$6.20 > $5.00" {
		t.Fatalf("detail = %q", d.Reasons[0].Detail)
	}
}

func TestEvaluateUndoneForcesAsk(t *testing.T) {
	d := Evaluate(Input{Mode: ModeAuto, Undone: true})
	if d.Decision != DecisionAsk || d.Reasons[0].Code != ReasonUndone {
		t.Fatalf("got %+v", d)
	}
}

func TestApprovalNote(t *testing.T) {
	cases := []struct {
		mode  Mode
		flags []string
		v     string
		hold  int
		want  string
	}{
		{ModeOnFlags, nil, "pass", 600, "Auto-approved: gate set to Ask on flags; no flags; verify passed; held 10m"},
		{ModeOnFlags, []string{"x", "y"}, "", 0, "Auto-approved: gate set to Ask on flags; no blocking flags (x, y); no verify; no hold"},
		{ModeAuto, []string{"x"}, "pass", 30, "Auto-approved: gate set to Auto; flags ignored; verify passed; held 30s"},
	}
	for _, c := range cases {
		if got := ApprovalNote(c.mode, c.flags, c.v, c.hold); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}
