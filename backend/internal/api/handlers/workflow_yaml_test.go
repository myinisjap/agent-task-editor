package handlers

import "testing"

func TestYamlWorkflow_Validate_CreatePR(t *testing.T) {
	mk := func(createPRLabels ...bool) yamlWorkflow {
		wf := yamlWorkflow{Name: "wf"}
		for i, cp := range createPRLabels {
			wf.Labels = append(wf.Labels, yamlLabel{Name: string(rune('a' + i)), CreatePR: cp})
		}
		return wf
	}

	cases := []struct {
		name    string
		wf      yamlWorkflow
		wantErr bool
	}{
		{"none", mk(false, false), false},
		{"one", mk(false, true, false), false},
		{"two", mk(true, false, true), true},
		{"all", mk(true, true), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.wf.validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestYamlWorkflow_Validate_Color(t *testing.T) {
	cases := []struct {
		name    string
		color   string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"short hex", "#fff", false},
		{"long hex uppercase", "#FFFFFF", false},
		{"eight digit hex", "#11223344", false},
		{"invalid hex chars", "#ggg", true},
		{"named color rejected", "red", true},
		{"missing hash", "fff", true},
		{"svg breakout payload rejected", `#fff"/></svg><img src=x onerror=alert(1)>`, true},
		{"css injection rejected", "#fff; background:url(x)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := yamlWorkflow{Name: "wf", Labels: []yamlLabel{{Name: "todo", Color: tc.color}}}
			err := wf.validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestYamlWorkflow_Validate_WipLimit(t *testing.T) {
	intp := func(v int) *int { return &v }

	cases := []struct {
		name    string
		limit   *int
		wantErr bool
	}{
		{"unset (unlimited)", nil, false},
		{"positive", intp(5), false},
		{"zero rejected", intp(0), true},
		{"negative rejected", intp(-1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := yamlWorkflow{Name: "wf", Labels: []yamlLabel{{Name: "review", WipLimit: tc.limit}}}
			err := wf.validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
