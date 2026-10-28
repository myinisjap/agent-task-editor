package autonomy

import (
	"reflect"
	"testing"
)

func globs(s Suggestions) []string {
	out := []string{}
	for _, g := range s.PathGlobs {
		out = append(out, g.Glob)
	}
	return out
}

func TestSuggestDirs(t *testing.T) {
	s := Suggest([]string{"a/b/c/x.go", "a/b/c/y.go", "a/b/d/z.go"}, 0, "")
	if want := []string{"a/b/**", "a/b/c/**", "a/b/d/**"}; !reflect.DeepEqual(globs(s), want) {
		t.Fatalf("got %v", globs(s))
	}
	if s.PathGlobs[0].Files != 3 || s.PathGlobs[1].Files != 2 || s.PathGlobs[0].Kind != "dir" {
		t.Fatalf("%+v", s.PathGlobs)
	}
	// Single deep file → deepest directory only.
	s = Suggest([]string{"a/b/c/x.go"}, 0, "")
	if want := []string{"a/b/c/**"}; !reflect.DeepEqual(globs(s), want) {
		t.Fatalf("got %v", globs(s))
	}
	// Root files give nothing; top 3 cap.
	s = Suggest([]string{"README.md", "go.mod"}, 0, "")
	if len(s.PathGlobs) != 0 {
		t.Fatalf("root: %v", globs(s))
	}
	s = Suggest([]string{"a/1", "b/1", "c/1", "d/1"}, 0, "")
	if want := []string{"a/**", "b/**", "c/**"}; !reflect.DeepEqual(globs(s), want) {
		t.Fatalf("ties: %v", globs(s))
	}
}

func TestSuggestExtTypeSize(t *testing.T) {
	s := Suggest([]string{"x.go", "db/a.sql", "db/b.sql", "ci/p.yml", "main.go"}, 347, "bug")
	var exts []string
	for _, g := range s.PathGlobs {
		if g.Kind == "ext" {
			exts = append(exts, g.Glob)
		}
	}
	if want := []string{"*.sql", "*.yml"}; !reflect.DeepEqual(exts, want) {
		t.Fatalf("exts %v", exts)
	}
	if s.TaskType == nil || *s.TaskType != "bug" {
		t.Fatal("type")
	}
	if s.MinChangedLines == nil || *s.MinChangedLines != 300 {
		t.Fatal("min")
	}
	for in, want := range map[int]int{10: 10, 58: 50, 99: 90, 100: 100, 1234: 1000, 12345: 10000} {
		if v := Suggest(nil, in, "").MinChangedLines; v == nil || *v != want {
			t.Errorf("%d → %v want %d", in, v, want)
		}
	}
	if Suggest(nil, 9, "").MinChangedLines != nil || Suggest(nil, 0, " ").TaskType != nil {
		t.Fatal("expected nils")
	}
}
