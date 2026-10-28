package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestGlobsOverlap(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"backend/a/**"}, []string{"backend/a/**"}, true},
		{[]string{"backend/a/**"}, []string{"backend/a/sub/**"}, true},
		{[]string{"backend/a/sub/**"}, []string{"backend/a/**"}, true},
		{[]string{"backend/a/**"}, []string{"backend/b/**"}, false},
		{[]string{"frontend/**"}, []string{"backend/**"}, false},
		{[]string{"backend/a/x.go"}, []string{"backend/a/**"}, true},
		{[]string{"backend/a/x.go"}, []string{"backend/b/**"}, false},
		{[]string{"*.go"}, []string{"backend/b/**"}, true}, // conservative
		{[]string{"docs/"}, []string{"docs/api.md"}, true},
		{[]string{"a/**", "b/**"}, []string{"c/**", "b/x/**"}, true},
		{nil, []string{"a/**"}, false},
	}
	for _, c := range cases {
		if got := GlobsOverlap(c.a, c.b); got != c.want {
			t.Errorf("GlobsOverlap(%v,%v)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestOutOfScope(t *testing.T) {
	got := OutOfScope([]string{"a/x", "b/y", "a/z/w"}, []string{"a/**"})
	if !reflect.DeepEqual(got, []string{"b/y"}) {
		t.Fatalf("got %v", got)
	}
	if len(OutOfScope(nil, []string{"a/**"})) != 0 {
		t.Fatal("no files => nothing out of scope")
	}
}

func TestNormalizeFileScope(t *testing.T) {
	got, err := NormalizeFileScope([]string{" a/** ", "a/**", "", "b/c.go"})
	if err != nil || !reflect.DeepEqual(got, []string{"a/**", "b/c.go"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	bad := [][]string{{"/abs"}, {"a/../b"}, {"a\nb"}, {"a\x00"}, {strings.Repeat("x", 257)}}
	for _, b := range bad {
		if _, err := NormalizeFileScope(b); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = strings.Repeat("d", i+1) + "/**"
	}
	if _, err := NormalizeFileScope(many); err == nil {
		t.Error("expected too-many error")
	}
}
