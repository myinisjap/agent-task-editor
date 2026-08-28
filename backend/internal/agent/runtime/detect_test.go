package runtime

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDetect_GoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.21\n\nrequire something\n")

	got := Detect(dir)
	if len(got) != 1 {
		t.Fatalf("expected 1 suggestion, got %+v", got)
	}
	if got[0] != (Suggestion{ID: "go", Version: "1.21", Source: "go.mod"}) {
		t.Errorf("got %+v", got[0])
	}
}

func TestDetect_NvmrcAndPythonVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "22\n")
	writeFile(t, dir, ".python-version", "3.12\n")

	got := Detect(dir)
	byID := map[string]Suggestion{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 suggestions, got %+v", got)
	}
	if byID["node"] != (Suggestion{ID: "node", Version: "22", Source: ".nvmrc"}) {
		t.Errorf("node = %+v", byID["node"])
	}
	if byID["python"] != (Suggestion{ID: "python", Version: "3.12", Source: ".python-version"}) {
		t.Errorf("python = %+v", byID["python"])
	}
}

func TestDetect_RustToolchainTOML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "rust-toolchain.toml", "[toolchain]\nchannel = \"1.75.0\"\n")

	got := Detect(dir)
	if len(got) != 1 || got[0] != (Suggestion{ID: "rust", Version: "1.75.0", Source: "rust-toolchain.toml"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDetect_RubyAndJava(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".ruby-version", "3.3.0\n")
	writeFile(t, dir, ".java-version", "21\n")

	got := Detect(dir)
	if len(got) != 2 {
		t.Fatalf("expected 2 suggestions, got %+v", got)
	}
}

func TestDetect_SkipsSymlinkedManifest(t *testing.T) {
	dir := t.TempDir()
	realTarget := filepath.Join(dir, "real-nvmrc")
	writeFile(t, dir, "real-nvmrc", "22\n")

	symlinkPath := filepath.Join(dir, ".nvmrc")
	if err := os.Symlink(realTarget, symlinkPath); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	got := Detect(dir)
	if len(got) != 0 {
		t.Fatalf("expected symlinked manifest to be skipped, got %+v", got)
	}
}

func TestDetect_NoManifests(t *testing.T) {
	dir := t.TempDir()
	got := Detect(dir)
	if len(got) != 0 {
		t.Fatalf("expected no suggestions, got %+v", got)
	}
}

func TestDetect_InvalidVersionDropped(t *testing.T) {
	dir := t.TempDir()
	// A go.mod whose directive version somehow fails the version regex
	// (e.g. contains a space via a malformed directive) must be dropped,
	// not surfaced as a bad suggestion.
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo \n")

	got := Detect(dir)
	if len(got) != 0 {
		t.Fatalf("expected invalid version to be dropped, got %+v", got)
	}
}

func TestDetect_Deterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n\ngo 1.22\n")
	writeFile(t, dir, ".nvmrc", "20\n")

	got1 := Detect(dir)
	got2 := Detect(dir)

	sortByID := func(s []Suggestion) {
		sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID })
	}
	sortByID(got1)
	sortByID(got2)

	if len(got1) != len(got2) {
		t.Fatalf("non-deterministic result lengths: %d vs %d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Errorf("non-deterministic at %d: %+v vs %+v", i, got1[i], got2[i])
		}
	}
}

func TestDetect_SubdirectoryManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "backend"), "go.mod", "module example.com/foo\n\ngo 1.26\n")

	got := Detect(dir)
	if len(got) != 1 {
		t.Fatalf("expected 1 suggestion, got %+v", got)
	}
	if got[0] != (Suggestion{ID: "go", Version: "1.26", Source: "backend/go.mod"}) {
		t.Errorf("got %+v", got[0])
	}
}

func TestDetect_RootWinsOverSubdirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.21\n")
	if err := os.Mkdir(filepath.Join(dir, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "svc"), "go.mod", "module example.com/svc\n\ngo 1.26\n")

	got := Detect(dir)
	if len(got) != 1 || got[0].Version != "1.21" || got[0].Source != "go.mod" {
		t.Errorf("root manifest should win, got %+v", got)
	}
}

func TestDetect_SkipsDotAndDependencyDirs(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{".hidden", "node_modules", "vendor"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, sub), ".nvmrc", "22\n")
	}

	if got := Detect(dir); len(got) != 0 {
		t.Errorf("manifests in dot/dependency dirs must be ignored, got %+v", got)
	}
}

func TestDetect_SkipsSymlinkedSubdirectory(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "go.mod", "module example.com/outside\n\ngo 1.26\n")
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlink: %v", err)
	}

	if got := Detect(dir); len(got) != 0 {
		t.Errorf("symlinked subdirectory must not be followed, got %+v", got)
	}
}

func TestDetect_TwoSubdirsDeterministic(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"api", "web"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "api"), "go.mod", "module example.com/api\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "web"), ".nvmrc", "22\n")
	// Same language in a later-sorted dir: first sorted dir wins.
	writeFile(t, filepath.Join(dir, "web"), "go.mod", "module example.com/web\n\ngo 1.21\n")

	got := Detect(dir)
	if len(got) != 2 {
		t.Fatalf("expected 2 suggestions, got %+v", got)
	}
	byID := map[string]Suggestion{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if byID["go"].Source != "api/go.mod" || byID["go"].Version != "1.26" {
		t.Errorf("go should come from api/ (sorted first), got %+v", byID["go"])
	}
	if byID["node"].Source != "web/.nvmrc" || byID["node"].Version != "22" {
		t.Errorf("node suggestion wrong: %+v", byID["node"])
	}
}
