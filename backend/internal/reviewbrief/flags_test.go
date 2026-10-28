package reviewbrief

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func fc(path, status string, added, removed []string) FileChange {
	return FileChange{Path: path, OldPath: path, Status: status, Added: len(added), Removed: len(removed), AddedLines: added, RemovedLines: removed}
}

func codes(fl []Flag) []string {
	out := []string{}
	for _, f := range fl {
		out = append(out, f.Code)
	}
	return out
}

func manyLines(n int) []string { return make([]string, n) }

func TestComputeFlags(t *testing.T) {
	cases := []struct {
		name  string
		files []FileChange
		plan  string
		want  []string
	}{
		{"migration dir", []FileChange{fc("backend/migrations/001.sql", "added", []string{"x"}, nil)}, "", []string{"migration"}},
		{"schema sql", []FileChange{fc("db/schema.sql", "modified", []string{"x"}, nil)}, "", []string{"migration"}},
		{"ddl in sql", []FileChange{fc("db/q.sql", "modified", []string{"ALTER TABLE x ADD y"}, nil)}, "", []string{"migration"}},
		{"queries sql no ddl", []FileChange{fc("backend/internal/storage/queries/tasks.sql", "modified", []string{"SELECT 1", "INSERT INTO t VALUES (1)"}, nil)}, "", []string{}},
		{"auth path", []FileChange{fc("backend/internal/api/middleware/cors.go", "modified", []string{"x"}, nil)}, "", []string{"auth_or_security"}},
		{".env", []FileChange{fc("deploy/.env.example", "modified", []string{"x"}, nil)}, "", []string{"auth_or_security"}},
		// Accepted false positive: substring matching flags tokenizer.go.
		{"tokenizer false positive", []FileChange{fc("lib/tokenizer.go", "modified", []string{"x"}, nil)}, "", []string{"auth_or_security"}},
		{"go.mod", []FileChange{fc("backend/go.mod", "modified", []string{"x"}, nil)}, "", []string{"dependencies"}},
		{"requirements txt", []FileChange{fc("requirements-dev.txt", "modified", []string{"x"}, nil)}, "", []string{"dependencies"}},
		{"dockerfile FROM", []FileChange{fc("Dockerfile", "modified", []string{"FROM golang:1.23"}, []string{"FROM golang:1.22"})}, "", []string{"dependencies"}},
		{"dockerfile no FROM", []FileChange{fc("Dockerfile", "modified", []string{"RUN echo"}, nil)}, "", []string{}},
		{"test deleted", []FileChange{fc("foo_test.go", "deleted", nil, []string{"x"})}, "", []string{"tests_removed"}},
		{"test net removed", []FileChange{fc("foo_test.go", "modified", []string{"func TestA(t *testing.T) {"}, []string{"func TestB(t *testing.T) {", "func TestC(t *testing.T) {"})}, "", []string{"tests_removed"}},
		{"test skip added", []FileChange{fc("a.test.ts", "modified", []string{"  it.skip('x', () => {})"}, nil)}, "", []string{"tests_removed"}},
		{"t.Skip added", []FileChange{fc("foo_test.go", "modified", []string{"\tt.Skip(\"later\")"}, nil)}, "", []string{"tests_removed"}},
		{"test modified with tests added", []FileChange{fc("foo_test.go", "modified", []string{"func TestA(t *testing.T) {", "func TestB(t *testing.T) {"}, []string{"func TestA(t *testing.T) {"})}, "", []string{}},
		{"ci", []FileChange{fc(".github/workflows/ci.yml", "modified", []string{"x"}, nil)}, "", []string{"ci_config"}},
		{"799 lines", []FileChange{fc("a.go", "modified", manyLines(799), nil)}, "", []string{}},
		{"801 lines", []FileChange{fc("a.go", "modified", manyLines(801), nil)}, "", []string{"large_change"}},
		{"lockfile lines ignored", []FileChange{fc("frontend/package-lock.json", "modified", manyLines(5000), nil), fc("go.sum", "modified", manyLines(900), nil)}, "", []string{"dependencies"}},
		{"plan outside", []FileChange{fc("a/x.go", "modified", []string{"x"}, nil), fc("b/y.go", "modified", []string{"x"}, nil)}, "- `a/x.go`", []string{"outside_plan_scope"}},
		{"plan inside w/ exemptions", []FileChange{fc("a/x.go", "modified", []string{"x"}, nil), fc("a/x_test.go", "added", []string{"x"}, nil), fc("CHANGELOG.md", "modified", []string{"x"}, nil)}, "- `a/x.go`", []string{}},
		{"plan dir and glob", []FileChange{fc("web/src/z.ts", "modified", []string{"x"}, nil), fc("a/b/c.go", "modified", []string{"x"}, nil)}, "- `web/src/`\n- `a/**/*.go`", []string{}},
		{"no plan file list", []FileChange{fc("b/y.go", "modified", []string{"x"}, nil)}, "Do the thing carefully.", []string{}},
		{"openapi exemption", []FileChange{fc("openapi.yaml", "modified", []string{"x"}, nil), fc("frontend/src/api/types.ts", "modified", []string{"x"}, nil)}, "- `openapi.yaml`", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codes(ComputeFlags(tc.files, tc.plan))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("flags = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeFlags_OrderStable(t *testing.T) {
	files := []FileChange{
		fc("zz/other.go", "modified", manyLines(900), nil),
		fc(".github/workflows/ci.yml", "modified", []string{"x"}, nil),
		fc("foo_test.go", "deleted", nil, []string{"x"}),
		fc("go.mod", "modified", []string{"x"}, nil),
		fc("auth/login.go", "modified", []string{"x"}, nil),
		fc("db/migrations/1.sql", "added", []string{"x"}, nil),
	}
	got := codes(ComputeFlags(files, "- `auth/login.go`"))
	want := []string{"migration", "auth_or_security", "dependencies", "tests_removed", "ci_config", "large_change", "outside_plan_scope"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestFlagFilesCapped(t *testing.T) {
	var files []FileChange
	for i := 0; i < 15; i++ {
		files = append(files, fc(fmt.Sprintf("db/migrations/%d.sql", i), "added", []string{"x"}, nil))
	}
	fl := ComputeFlags(files, "")
	if len(fl) == 0 || len(fl[0].Files) != 10 {
		t.Fatalf("files not capped: %+v", fl)
	}
}

func TestPlanFiles(t *testing.T) {
	plan := "## Plan\n- Edit `backend/internal/foo.go:12-30` and `README.md`\n- Add ./frontend/src/App.tsx, then **openapi.yaml**.\n- touch `web/dir/` and `src/**/*.ts`\nUse and/or e.g. v1.2 here. `go test ./...`\nSee https://example.com/a.b"
	got := PlanFiles(plan)
	want := []string{"backend/internal/foo.go", "README.md", "web/dir/", "src/**/*.ts", "frontend/src/App.tsx", "openapi.yaml"}
	have := strings.Join(got, ",")
	for _, w := range want {
		if !strings.Contains(","+have+",", ","+w+",") {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	for _, bad := range []string{"and/or", "e.g", "v1.2", "./...", "https://example.com/a.b"} {
		for _, g := range got {
			if g == bad {
				t.Errorf("unexpected %q", bad)
			}
		}
	}
	if len(PlanFiles("nothing here")) != 0 {
		t.Error("expected none")
	}
}

func TestFlagCodesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range FlagCodes {
		seen[c] = true
	}
	if len(FlagCodes) != 7 || len(seen) != 7 {
		t.Fatalf("FlagCodes = %v", FlagCodes)
	}
}
