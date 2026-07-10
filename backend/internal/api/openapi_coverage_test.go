package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// validHTTPMethods is the set of YAML keys under a path item that represent
// an HTTP method (as opposed to sibling keys like "parameters", "summary",
// or "description" that can appear at the path-item level).
var validHTTPMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true,
	"delete": true, "head": true, "options": true,
}

// nonAPIRoutes lists routes served outside the /api/v1 prefix (and thus not
// documented relative to the spec's "servers:" base URL). These are
// special-cased by their full router pattern rather than stripped of a
// prefix.
var nonAPIRoutes = map[string]bool{
	"GET /ws":      true,
	"GET /metrics": true,
	"GET /healthz": true,
}

// openAPIRepoRootPath locates openapi.yaml relative to this test file's own
// location (backend/internal/api/openapi_coverage_test.go), so the test is
// robust regardless of the working directory `go test` is invoked from.
func openAPIRepoRootPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path via runtime.Caller")
	}
	// this file: <repo>/backend/internal/api/openapi_coverage_test.go
	// openapi.yaml: <repo>/openapi.yaml
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "openapi.yaml")
}

// openAPIPathMethods parses openapi.yaml's `paths:` block into a map of
// path -> set of lowercase HTTP methods documented for it. Only presence of
// method keys is checked — schema bodies are ignored.
func openAPIPathMethods(t *testing.T) map[string]map[string]bool {
	t.Helper()

	data, err := os.ReadFile(openAPIRepoRootPath(t))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	out := make(map[string]map[string]bool, len(doc.Paths))
	for path, item := range doc.Paths {
		methods := make(map[string]bool)
		for key := range item {
			lower := strings.ToLower(key)
			if validHTTPMethods[lower] {
				methods[lower] = true
			}
		}
		out[path] = methods
	}
	return out
}

// TestOpenAPISpecCoversAllRoutes walks the live router with chi.Walk and
// fails, listing every offender, if any served route is missing from
// openapi.yaml. This closes the loop on the existing gen:api / sqlc
// codegen-drift checks: those enforce that the spec's *output* (generated TS
// types / SQL code) stays committed, but nothing previously enforced that
// the spec itself stays in sync with the router it's meant to describe. See
// issue #140.
func TestOpenAPISpecCoversAllRoutes(t *testing.T) {
	r := newTestRouter(t, "test-token", "")

	routes, ok := r.(chi.Routes)
	if !ok {
		t.Fatalf("router does not implement chi.Routes (got %T)", r)
	}

	specPaths := openAPIPathMethods(t)

	var missing []string
	walkErr := chi.Walk(routes, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		method = strings.ToUpper(method)
		full := method + " " + pattern

		if nonAPIRoutes[full] {
			return nil
		}

		if !strings.HasPrefix(pattern, "/api/v1") {
			missing = append(missing, full+" (not under /api/v1 and not in the exclusion list — add spec coverage or an explicit exclusion)")
			return nil
		}
		specPath := strings.TrimPrefix(pattern, "/api/v1")

		methods, ok := specPaths[specPath]
		if !ok {
			missing = append(missing, full+" (path "+specPath+" absent from openapi.yaml)")
			return nil
		}
		if !methods[strings.ToLower(method)] {
			missing = append(missing, full+" (path "+specPath+" present but missing method "+method+")")
			return nil
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk: %v", walkErr)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("openapi.yaml is missing %d served route(s):\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
