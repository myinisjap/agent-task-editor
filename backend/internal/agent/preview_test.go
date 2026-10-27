package agent

import (
	"reflect"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestExpandBraces(t *testing.T) {
	cases := map[string][]string{
		"a/**":               {"a/**"},
		"src/**/*.{tsx,jsx}": {"src/**/*.tsx", "src/**/*.jsx"},
		"{a,b}/{c,d}":        {"a/c", "a/d", "b/c", "b/d"},
		"unterminated/{a,b":  {"unterminated/{a,b"},
	}
	for in, want := range cases {
		if got := expandBraces(in); !reflect.DeepEqual(got, want) {
			t.Errorf("expandBraces(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPreviewGlobs_Defaults(t *testing.T) {
	if got := PreviewGlobs(" \n\n"); !reflect.DeepEqual(got, DefaultPreviewGlobs) {
		t.Errorf("empty setting should use defaults, got %v", got)
	}
	if got := PreviewGlobs("a/**\r\n b/** \n"); !reflect.DeepEqual(got, []string{"a/**", "b/**"}) {
		t.Errorf("got %v", got)
	}
}

func TestMatchesPreviewGlobs(t *testing.T) {
	d := DefaultPreviewGlobs
	cases := []struct {
		file  string
		globs []string
		want  bool
	}{
		{"frontend/src/App.tsx", d, true},
		{"src/components/x.vue", d, true},
		{"src/main.go", d, false},
		{"backend/x.go", d, false},
		{"web/index.html", d, true},
		{"docs/readme.md", d, false},
		{"backend/x.go", []string{"backend/**"}, true},
		{"frontend/src/App.tsx", []string{"backend/**"}, false},
	}
	for _, c := range cases {
		if got := MatchesPreviewGlobs([]string{c.file}, c.globs); got != c.want {
			t.Errorf("MatchesPreviewGlobs(%q, %v) = %v, want %v", c.file, c.globs, got, c.want)
		}
	}
	if MatchesPreviewGlobs(nil, d) {
		t.Error("no files should never match")
	}
}

func TestParsePreviewRoutes(t *testing.T) {
	got := ParsePreviewRoutes("/\r\n /tasks \n\n/tasks/inbox\n")
	if want := []string{"/", "/tasks", "/tasks/inbox"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestShouldCapturePreview(t *testing.T) {
	ui := []string{"frontend/src/App.tsx"}
	ok := gen.Repo{PreviewCommand: "npm run dev", PreviewRoutes: "/\n/tasks"}
	if routes, yes := shouldCapturePreview(ok, ui); !yes || len(routes) != 2 {
		t.Errorf("configured+matching = %v %v", routes, yes)
	}
	noCmd := ok
	noCmd.PreviewCommand = "  "
	noRoutes := ok
	noRoutes.PreviewRoutes = ""
	for name, tc := range map[string]struct {
		repo  gen.Repo
		files []string
	}{
		"blank command": {noCmd, ui},
		"no routes":     {noRoutes, ui},
		"no match":      {ok, []string{"backend/main.go"}},
		"unset":         {gen.Repo{}, ui},
	} {
		if _, yes := shouldCapturePreview(tc.repo, tc.files); yes {
			t.Errorf("%s: should be a no-op", name)
		}
	}
}
