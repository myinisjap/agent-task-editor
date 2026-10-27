package intake

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func p(t *testing.T, s string) any {
	t.Helper()
	v, err := ParsePayload([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRender_Paths(t *testing.T) {
	pl := p(t, `{"alert":{"title":"boom","n":12345678901234567890,"ok":true,"nil":null,"obj":{"a":1}},"items":["x","y"]}`)
	cases := map[string]string{
		"{{alert.title}}":        "boom",
		"{{.alert.title}}":       "boom",
		"{{ alert.title }}":      "boom",
		"{{items.1}}":            "y",
		"{{items.5}}":            "",
		"{{alert.missing}}":      "",
		"{{alert.n}}":            "12345678901234567890",
		"{{alert.ok}}":           "true",
		"{{alert.nil}}":          "",
		"{{alert.obj}}":          `{"a":1}`,
		"{{alert.title.deeper}}": "",
		"{ {alert.title} }":      "{ {alert.title} }",
		`{{printf "x"}}`:         `{{printf "x"}}`,
		"{{call .x}}":            "{{call .x}}",
		"a {{alert.title}} b":    "a boom b",
	}
	for in, want := range cases {
		if got := Render(in, pl, 1000); got != want {
			t.Errorf("Render(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRender_Caps(t *testing.T) {
	pl := p(t, `{"big":"`+strings.Repeat("a", 10000)+`","u":"`+strings.Repeat("é", 100)+`"}`)
	if got := Render("{{big}}", pl, 100000); len(got) > MaxValueLen {
		t.Errorf("value cap exceeded: %d", len(got))
	}
	got := Render("{{u}}", pl, 51)
	if len(got) > 51 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Errorf("bad truncation %q (%d)", got, len(got))
	}
}

func TestRenderTitle(t *testing.T) {
	pl := p(t, `{"t":"line1\nline2\t\u0007x"}`)
	if got := RenderTitle("{{t}}", pl, "ep"); got != "line1 line2 x" {
		t.Errorf("got %q", got)
	}
	if got := RenderTitle("{{nope}}", pl, "ep"); got != "Intake event: ep" {
		t.Errorf("got %q", got)
	}
	if got := RenderTitle(strings.Repeat("z", 500), pl, "ep"); len(got) > MaxTitleLen {
		t.Errorf("title too long: %d", len(got))
	}
}

func TestRenderDescription(t *testing.T) {
	pl := p(t, `{"a":"b"}`)
	d := RenderDescription("", pl, "ep")
	if !strings.Contains(d, "```json") || !strings.HasSuffix(d, "_Received via intake endpoint ep_") {
		t.Errorf("default description: %q", d)
	}
	d = RenderDescription("x={{a}}", pl, "ep")
	if !strings.HasPrefix(d, "x=b\n\n") {
		t.Errorf("got %q", d)
	}
}

func TestRenderDedupeKey(t *testing.T) {
	pl := p(t, `{"a":"b"}`)
	if RenderDedupeKey("", pl) != "" || RenderDedupeKey("k-{{a}}", pl) != "k-b" {
		t.Error("dedupe key")
	}
}

func TestParsePayload_Trailing(t *testing.T) {
	if _, err := ParsePayload([]byte(`{} {}`)); err == nil {
		t.Error("expected error")
	}
	if _, err := ParsePayload([]byte(`{`)); err == nil {
		t.Error("expected error")
	}
}
