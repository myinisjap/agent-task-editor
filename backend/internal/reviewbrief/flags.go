package reviewbrief

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Risk flag codes. These are a stable contract (API, UI, and later auto-approve
// policies build on them); do not rename or reorder.
const (
	FlagMigration        = "migration"
	FlagAuthOrSecurity   = "auth_or_security"
	FlagDependencies     = "dependencies"
	FlagTestsRemoved     = "tests_removed"
	FlagCIConfig         = "ci_config"
	FlagLargeChange      = "large_change"
	FlagOutsidePlanScope = "outside_plan_scope"
)

// Thresholds for FlagLargeChange (strictly greater than).
const (
	LargeChangeLines = 800
	LargeChangeFiles = 25
)

const maxFlagFiles = 10

// Flag is one raised risk flag. Flags are advisory only.
type Flag struct {
	Code   string   `json:"code"`
	Reason string   `json:"reason"`
	Files  []string `json:"files"`
}

// ComputeFlags applies every rule to the parsed diff and returns the raised
// flags in the fixed order of the Flag* constants above. planText is the task's
// agent_notes (the plan lives there); it only matters for outside_plan_scope.
func ComputeFlags(files []FileChange, planText string) []Flag {
	out := []Flag{}
	rules := []func([]FileChange, string) *Flag{
		ruleMigration, ruleAuth, ruleDependencies, ruleTestsRemoved,
		ruleCI, ruleLarge, ruleOutsidePlan,
	}
	for _, r := range rules {
		if f := r(files, planText); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func newFlag(code, reason string, files []string) *Flag {
	if files == nil {
		files = []string{}
	}
	if len(files) > maxFlagFiles {
		files = files[:maxFlagFiles]
	}
	return &Flag{Code: code, Reason: reason, Files: files}
}

func lpath(f FileChange) string { return strings.ToLower(f.Path) }

func anyLineMatches(f FileChange, re *regexp.Regexp) bool {
	for _, l := range f.AddedLines {
		if re.MatchString(l) {
			return true
		}
	}
	for _, l := range f.RemovedLines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// ---- migration ----

var ddlRe = regexp.MustCompile(`(?i)^\s*(create|alter|drop)\s+(table|index|view|trigger)`)

func ruleMigration(files []FileChange, _ string) *Flag {
	var hit []string
	for _, f := range files {
		p := lpath(f)
		isMig := false
		for _, seg := range strings.Split(p, "/") {
			if seg == "migrations" {
				isMig = true
			}
		}
		if !isMig && strings.HasSuffix(p, ".sql") {
			if strings.Contains(path.Base(p), "schema") || anyLineMatches(f, ddlRe) {
				isMig = true
			}
		}
		if isMig {
			hit = append(hit, f.Path)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return newFlag(FlagMigration, fmt.Sprintf("%d migration/schema file(s) changed", len(hit)), hit)
}

// ---- auth_or_security ----

// authKeywords are substring-matched against the lowercased path. Substring
// matching over-flags (e.g. tokenizer.go); that is accepted: flags are advisory.
var authKeywords = []string{
	"auth", "oauth", "token", "secret", "middleware", "crypto", "password",
	"credential", "jwt", "csrf", "cors", "permission", "security", "session",
}

func ruleAuth(files []FileChange, _ string) *Flag {
	var hit []string
	for _, f := range files {
		p := lpath(f)
		match := strings.HasPrefix(path.Base(p), ".env")
		for _, k := range authKeywords {
			if strings.Contains(p, k) {
				match = true
				break
			}
		}
		if match {
			hit = append(hit, f.Path)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return newFlag(FlagAuthOrSecurity, fmt.Sprintf("%d auth/security-related file(s) changed", len(hit)), hit)
}

// ---- dependencies ----

var depBasenames = map[string]bool{
	"go.mod": true, "go.sum": true, "package.json": true, "package-lock.json": true,
	"npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true,
	"cargo.toml": true, "cargo.lock": true, "pipfile": true, "pipfile.lock": true,
	"poetry.lock": true, "pyproject.toml": true, "uv.lock": true, "gemfile": true, "gemfile.lock": true,
}

var fromRe = regexp.MustCompile(`(?i)^\s*FROM\s`)

func isDockerfile(base string) bool {
	return base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || strings.HasSuffix(base, ".dockerfile")
}

func ruleDependencies(files []FileChange, _ string) *Flag {
	var hit []string
	for _, f := range files {
		p := lpath(f)
		base := path.Base(p)
		match := depBasenames[base]
		if !match && strings.HasSuffix(base, ".txt") {
			if strings.HasPrefix(base, "requirements") {
				match = true
			} else {
				for _, seg := range strings.Split(path.Dir(p), "/") {
					if seg == "requirements" {
						match = true
					}
				}
			}
		}
		if !match && isDockerfile(base) && anyLineMatches(f, fromRe) {
			match = true
		}
		if match {
			hit = append(hit, f.Path)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return newFlag(FlagDependencies, fmt.Sprintf("%d dependency/base-image file(s) changed", len(hit)), hit)
}

// ---- tests_removed ----

var (
	testFileRe = regexp.MustCompile(`(_test\.go|\.test\.(ts|tsx|js|jsx|mjs)|\.spec\.(ts|tsx|js|jsx))$`)
	pyTestRe   = regexp.MustCompile(`^(test_.*|.*_test)\.py$`)
	testDeclRe = []*regexp.Regexp{
		regexp.MustCompile(`^\s*func Test\w*\(`),
		regexp.MustCompile(`^\s*(it|test|describe)(\.each\S*)?\(`),
		regexp.MustCompile(`^\s*(async\s+)?def test_`),
	}
	testSkipRe = []*regexp.Regexp{
		regexp.MustCompile(`t\.Skip(Now|f)?\(`),
		regexp.MustCompile(`\b(it|test|describe)\.(skip|only)\(`),
		regexp.MustCompile(`\bx(it|describe)\(`),
		regexp.MustCompile(`\bf(it|describe)\(`),
		regexp.MustCompile(`@pytest\.mark\.skip`),
	}
)

func isTestPath(p string) bool {
	p = strings.ToLower(p)
	if testFileRe.MatchString(p) || pyTestRe.MatchString(path.Base(p)) {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "__tests__" {
			return true
		}
	}
	return false
}

func countDecls(lines []string) int {
	n := 0
	for _, l := range lines {
		for _, re := range testDeclRe {
			if re.MatchString(l) {
				n++
				break
			}
		}
	}
	return n
}

func ruleTestsRemoved(files []FileChange, _ string) *Flag {
	var deleted, skipped, net []string
	declAdded, declRemoved := 0, 0
	for _, f := range files {
		wasTest := isTestPath(f.Path) || (f.Status == "renamed" && isTestPath(f.OldPath))
		if !wasTest {
			continue
		}
		if f.Status == "deleted" || (f.Status == "renamed" && !isTestPath(f.Path)) {
			deleted = append(deleted, f.Path)
		}
		a, r := countDecls(f.AddedLines), countDecls(f.RemovedLines)
		declAdded += a
		declRemoved += r
		if r > a {
			net = append(net, f.Path)
		}
		for _, l := range f.AddedLines {
			for _, re := range testSkipRe {
				if re.MatchString(l) {
					skipped = append(skipped, f.Path)
					goto nextFile
				}
			}
		}
	nextFile:
	}
	var reasons []string
	var hit []string
	if len(deleted) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d test file(s) deleted", len(deleted)))
		hit = append(hit, deleted...)
	}
	if declRemoved > declAdded {
		reasons = append(reasons, fmt.Sprintf("net %d test function(s) removed", declRemoved-declAdded))
		hit = append(hit, net...)
	}
	if len(skipped) > 0 {
		reasons = append(reasons, "skip/only marker added to tests")
		hit = append(hit, skipped...)
	}
	if len(reasons) == 0 {
		return nil
	}
	return newFlag(FlagTestsRemoved, strings.Join(reasons, "; "), dedupe(hit))
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- ci_config ----

func ruleCI(files []FileChange, _ string) *Flag {
	var hit []string
	for _, f := range files {
		p := lpath(f)
		if strings.HasPrefix(p, ".github/workflows/") || strings.HasPrefix(p, ".gitea/workflows/") ||
			p == ".gitlab-ci.yml" || strings.HasSuffix(p, "/.gitlab-ci.yml") {
			hit = append(hit, f.Path)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	return newFlag(FlagCIConfig, fmt.Sprintf("%d CI config file(s) changed", len(hit)), hit)
}

// ---- large_change ----

// isGeneratedOrLock reports files excluded from the large_change line count.
func isGeneratedOrLock(p string) bool {
	p = strings.ToLower(p)
	switch path.Base(p) {
	case "go.sum", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "poetry.lock", "uv.lock", "pipfile.lock", "gemfile.lock":
		return true
	}
	if strings.HasSuffix(p, "-lock.json") {
		return true
	}
	return p == "frontend/src/api/types.ts" || strings.HasSuffix(p, "/api/types.ts")
}

func ruleLarge(files []FileChange, _ string) *Flag {
	lines := 0
	for _, f := range files {
		if !isGeneratedOrLock(f.Path) {
			lines += f.Added + f.Removed
		}
	}
	if lines > LargeChangeLines || len(files) > LargeChangeFiles {
		return newFlag(FlagLargeChange,
			fmt.Sprintf("%d changed lines across %d files (thresholds: >%d lines or >%d files; lockfiles/generated excluded from lines)",
				lines, len(files), LargeChangeLines, LargeChangeFiles), nil)
	}
	return nil
}

// ---- outside_plan_scope ----

var (
	backtickRe = regexp.MustCompile("`([^`\\s]+)`")
	listItemRe = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)
	lineSufRe  = regexp.MustCompile(`:\d+(-\d+)?$`)
)

var plainExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".md": true,
	".yaml": true, ".yml": true, ".json": true, ".sql": true, ".mod": true, ".sum": true,
	".toml": true, ".txt": true, ".css": true, ".html": true, ".py": true, ".sh": true, ".lock": true,
}

func normalizePlanToken(tok string) string {
	tok = strings.TrimRight(tok, ".,:;")
	tok = strings.Trim(tok, "\"'()[]{}<>,;!? \t")
	if len(tok) > 4 && strings.HasPrefix(tok, "**") && strings.HasSuffix(tok, "**") {
		tok = tok[2 : len(tok)-2] // markdown bold
	}
	tok = strings.TrimPrefix(tok, "./")
	tok = lineSufRe.ReplaceAllString(tok, "")
	tok = strings.TrimRight(tok, ".,:;")
	return tok
}

func planPathLike(tok string) bool {
	if tok == "" || strings.Contains(tok, "://") || strings.ContainsAny(tok, " \t") {
		return false
	}
	if strings.Contains(tok, "/") {
		base := path.Base(strings.TrimSuffix(tok, "/"))
		return strings.HasSuffix(tok, "/") || strings.Contains(tok, "*") || strings.Contains(base, ".")
	}
	if tok == "Dockerfile" || tok == "Makefile" {
		return true
	}
	return plainExts[strings.ToLower(path.Ext(tok))]
}

// PlanFiles extracts path-like tokens from backtick spans and markdown list
// items of the plan text.
func PlanFiles(planText string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(raw string) {
		tok := normalizePlanToken(raw)
		if planPathLike(tok) && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	for _, line := range strings.Split(planText, "\n") {
		for _, m := range backtickRe.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
		if listItemRe.MatchString(line) {
			stripped := backtickRe.ReplaceAllString(line, " ")
			for _, tok := range strings.Fields(listItemRe.ReplaceAllString(stripped, "")) {
				add(tok)
			}
		}
	}
	return out
}

func globRegexp(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

func inPlan(p string, planned []string) bool {
	for _, pl := range planned {
		switch {
		case strings.HasSuffix(pl, "/"):
			if strings.HasPrefix(p, pl) || strings.Contains(p, "/"+pl) {
				return true
			}
		case strings.Contains(pl, "*"):
			if re := globRegexp(pl); re != nil && (re.MatchString(p) || re.MatchString(lastN(p, strings.Count(pl, "/")+1))) {
				return true
			}
		default:
			if p == pl || strings.HasSuffix(p, "/"+pl) {
				return true
			}
		}
	}
	return false
}

// lastN returns the last n path segments of p (so partial plan globs match).
func lastN(p string, n int) string {
	segs := strings.Split(p, "/")
	if n >= len(segs) {
		return p
	}
	return strings.Join(segs[len(segs)-n:], "/")
}

func hasPlanned(planned []string, pred func(string) bool) bool {
	for _, pl := range planned {
		if pred(pl) {
			return true
		}
	}
	return false
}

func exempt(p string, planned []string) bool {
	base := path.Base(p)
	if base == "CHANGELOG.md" {
		return true
	}
	// test sibling of a planned file
	for _, pl := range planned {
		ext := path.Ext(pl)
		if ext == "" || strings.Contains(pl, "*") || strings.HasSuffix(pl, "/") {
			continue
		}
		stem := strings.TrimSuffix(pl, ext)
		for _, sib := range []string{stem + "_test" + ext, stem + ".test" + ext, stem + ".spec" + ext} {
			if p == sib || strings.HasSuffix(p, "/"+sib) {
				return true
			}
		}
	}
	if p == "frontend/src/api/types.ts" && hasPlanned(planned, func(s string) bool { return path.Base(s) == "openapi.yaml" }) {
		return true
	}
	if strings.HasPrefix(p, "backend/internal/storage/gen/") && hasPlanned(planned, func(s string) bool {
		return strings.Contains(s, "storage/queries/") || strings.Contains(s, "storage/migrations/")
	}) {
		return true
	}
	if base == "go.sum" && hasPlanned(planned, func(s string) bool { return path.Base(s) == "go.mod" }) {
		return true
	}
	if isGeneratedOrLock(p) && base != "go.sum" && base != "types.ts" &&
		hasPlanned(planned, func(s string) bool { return path.Base(s) == "package.json" }) {
		return true
	}
	return false
}

func ruleOutsidePlan(files []FileChange, planText string) *Flag {
	planned := PlanFiles(planText)
	if len(planned) == 0 {
		return nil
	}
	var out []string
	for _, f := range files {
		if inPlan(f.Path, planned) || exempt(f.Path, planned) {
			continue
		}
		out = append(out, f.Path)
	}
	if len(out) == 0 {
		return nil
	}
	return newFlag(FlagOutsidePlanScope,
		fmt.Sprintf("%d changed file(s) not mentioned in the plan's file list (heuristic)", len(out)), out)
}
