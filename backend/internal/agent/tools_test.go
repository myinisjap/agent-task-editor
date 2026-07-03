package agent

import "testing"

func TestMatchCommandPattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		s       string
		want    bool
	}{
		{"exact match", "git status", "git status", true},
		{"exact mismatch", "git status", "git status --short", false},
		{"empty pattern never matches", "", "anything", false},
		{"prefix wildcard", "git *", "git commit -m x", true},
		{"prefix wildcard no match", "git *", "echo git commit", false},
		{"suffix wildcard", "* --force", "git push --force", true},
		{"suffix wildcard no match", "* --force", "git push --force-with-lease", false},
		{"middle wildcard", "npm * test", "npm run test", true},
		{"leading and trailing wildcard", "*rm -rf*", "sudo rm -rf /", true},
		{"bare wildcard matches anything", "*", "literally anything", true},
		{"no wildcard exact only", "go build", "go build ./...", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchCommandPattern(tc.pattern, tc.s); got != tc.want {
				t.Fatalf("matchCommandPattern(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
			}
		})
	}
}

func TestCommandPolicy_Allowed(t *testing.T) {
	t.Run("empty policy allows everything", func(t *testing.T) {
		p := CommandPolicy{}
		if ok, reason := p.Allowed("rm -rf /"); !ok {
			t.Fatalf("expected allowed, got denied: %s", reason)
		}
	})

	t.Run("denylist blocks even if allowlist would allow", func(t *testing.T) {
		p := CommandPolicy{
			Allowlist: []string{"rm *"},
			Denylist:  []string{"rm -rf *"},
		}
		if ok, _ := p.Allowed("rm -rf /"); ok {
			t.Fatalf("expected denied")
		}
		if ok, reason := p.Allowed("rm somefile"); !ok {
			t.Fatalf("expected allowed for non-denylisted allowlisted command, got denied: %s", reason)
		}
	})

	t.Run("allowlist-only blocks non-matching commands", func(t *testing.T) {
		p := CommandPolicy{Allowlist: []string{"git *", "npm test"}}
		if ok, _ := p.Allowed("git status"); !ok {
			t.Fatalf("expected allowed")
		}
		if ok, _ := p.Allowed("npm test"); !ok {
			t.Fatalf("expected allowed")
		}
		if ok, _ := p.Allowed("curl http://evil"); ok {
			t.Fatalf("expected denied")
		}
	})

	t.Run("allowlist and denylist combined", func(t *testing.T) {
		p := CommandPolicy{
			Allowlist: []string{"git *"},
			Denylist:  []string{"git push --force*"},
		}
		if ok, _ := p.Allowed("git status"); !ok {
			t.Fatalf("expected allowed")
		}
		if ok, _ := p.Allowed("git push --force"); ok {
			t.Fatalf("expected denied by denylist")
		}
		if ok, _ := p.Allowed("npm install"); ok {
			t.Fatalf("expected denied: not in allowlist")
		}
	})

	t.Run("trims whitespace before matching", func(t *testing.T) {
		p := CommandPolicy{Allowlist: []string{"git status"}}
		if ok, _ := p.Allowed("  git status  \n"); !ok {
			t.Fatalf("expected allowed after trimming")
		}
	})
}
