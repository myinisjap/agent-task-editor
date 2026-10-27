package intake

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits applied to webhook-rendered task fields and templates.
const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 16 << 10
	MaxDedupeKeyLen   = 512
	MaxTemplateLen    = 4 << 10
	// MaxValueLen caps a single substituted value.
	MaxValueLen = 4 << 10
)

var placeholderRe = regexp.MustCompile(`\{\{\s*\.?([A-Za-z0-9_\-]+(?:\.[A-Za-z0-9_\-]+)*)\s*\}\}`)

// ParsePayload decodes a JSON document preserving large numbers
// (json.Number), as Render expects.
func ParsePayload(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errTrailing
	}
	return v, nil
}

type parseError string

func (e parseError) Error() string { return string(e) }

const errTrailing = parseError("unexpected trailing data after JSON value")

// Render substitutes {{path.to.field}} placeholders (a leading dot is
// accepted) in tmpl with values from payload. It is deliberately NOT
// text/template: there are no functions, conditionals or loops, so a
// template can never execute anything. Anything that is not a plain
// placeholder is left literal. Missing paths render as "". Each substituted
// value is capped at MaxValueLen and the whole output at maxLen bytes
// (truncated on a rune boundary with a trailing ellipsis).
func Render(tmpl string, payload any, maxLen int) string {
	out := placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		sub := placeholderRe.FindStringSubmatch(m)
		return truncate(lookup(payload, sub[1]), MaxValueLen)
	})
	return truncate(out, maxLen)
}

func lookup(payload any, path string) string {
	cur := payload
	for _, seg := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return ""
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return ""
			}
			cur = v[i]
		default:
			return ""
		}
	}
	return stringify(cur)
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	const ell = "…"
	cut := max - len(ell)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ell
}

// SanitizeLine collapses whitespace controls to spaces, strips other control
// characters and trims, producing a single-line string.
func SanitizeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// RenderTitle renders a title template into a single capped line, falling
// back to "Intake event: <name>" when the result is empty.
func RenderTitle(tmpl string, payload any, name string) string {
	t := SanitizeLine(Render(tmpl, payload, MaxTitleLen*2))
	t = truncate(t, MaxTitleLen)
	if t == "" {
		return truncate("Intake event: "+SanitizeLine(name), MaxTitleLen)
	}
	return t
}

// RenderDescription renders the description template; when tmpl is empty the
// payload is pretty-printed in a json fence. A provenance footer is always
// appended.
func RenderDescription(tmpl string, payload any, name string) string {
	var body string
	if strings.TrimSpace(tmpl) == "" {
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			b = []byte("{}")
		}
		body = "```json\n" + truncate(string(b), MaxDescriptionLen-1024) + "\n```"
	} else {
		body = Render(tmpl, payload, MaxDescriptionLen-1024)
	}
	return body + "\n\n_Received via intake endpoint " + SanitizeLine(name) + "_"
}

// RenderDedupeKey renders the dedupe key template, single-line and capped.
func RenderDedupeKey(tmpl string, payload any) string {
	if tmpl == "" {
		return ""
	}
	return truncate(SanitizeLine(Render(tmpl, payload, MaxDedupeKeyLen*2)), MaxDedupeKeyLen)
}
