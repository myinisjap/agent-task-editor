package pathglob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, file string
		want      bool
	}{
		{"backend/**", "backend/a/b/x.go", true},
		{"backend/**", "frontend/x.go", false},
		{"**/x.go", "x.go", true},
		{"*.sql", "a/b/x.sql", true},
		{"*.sql", "x.sqlx", false},
		{"frontend/", "frontend/src/a.ts", true},
		{"frontend/", "frontendx/a.ts", false},
		{"a?.go", "a/.go", false},
		{"go.mod", "backend/go.mod", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := Match(c.pat, c.file); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.pat, c.file, got, c.want)
		}
	}
}

func TestMatcherAny(t *testing.T) {
	m := NewMatcher()
	if !m.MatchAny([]string{"x/**", "*.sql"}, []string{"a.go", "db/q.sql"}) {
		t.Fatal("expected match")
	}
	if m.MatchAny([]string{"x/**"}, []string{"a.go"}) {
		t.Fatal("unexpected match")
	}
	if Valid("") || !Valid("a/**") {
		t.Fatal("Valid wrong")
	}
}
