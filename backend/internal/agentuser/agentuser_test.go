package agentuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const bothCaps = uint64(1<<capSetuid | 1<<capSetgid | 1<<capKill)

func base() Inputs {
	return Inputs{AgentUID: "10001", AgentGID: "1000", EUID: 1000, CapEff: bothCaps, SetprivPath: "/usr/bin/setpriv"}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		mut     func(*Inputs)
		enabled bool
		reason  string
	}{
		{"enabled", func(*Inputs) {}, true, ""},
		{"opt-out", func(i *Inputs) { i.Isolation = "false" }, false, "AGENT_ISOLATION"},
		{"no uid", func(i *Inputs) { i.AgentUID = "" }, false, "AGENT_UID is not set"},
		{"bad uid", func(i *Inputs) { i.AgentUID = "x" }, false, "not a number"},
		{"root uid", func(i *Inputs) { i.AgentUID = "0" }, false, "root"},
		{"no gid", func(i *Inputs) { i.AgentGID = "" }, false, "AGENT_GID"},
		{"root gid", func(i *Inputs) { i.AgentGID = "0" }, false, "AGENT_GID is 0"},
		{"same user", func(i *Inputs) { i.EUID = 10001 }, false, "already runs"},
		{"no setuid cap", func(i *Inputs) { i.CapEff = 1 << capSetgid }, false, "CAP_SETUID"},
		{"no kill cap", func(i *Inputs) { i.CapEff = 1<<capSetuid | 1<<capSetgid }, false, "CAP_KILL"},
		{"no caps", func(i *Inputs) { i.CapEff = 0 }, false, "CAP_SETUID"},
		{"no setpriv non-root", func(i *Inputs) { i.SetprivPath = "" }, false, "setpriv"},
		{"no setpriv root ok", func(i *Inputs) { i.SetprivPath = ""; i.EUID = 0 }, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mut(&in)
			c := Decide(in)
			if c.Enabled != tc.enabled {
				t.Fatalf("Enabled=%v reason=%q", c.Enabled, c.Reason)
			}
			if !tc.enabled && !strings.Contains(c.Reason, tc.reason) {
				t.Fatalf("reason %q lacks %q", c.Reason, tc.reason)
			}
		})
	}
	c := Decide(base())
	if c.UID != 10001 || c.GID != 1000 || c.Home != "/home/agent" {
		t.Fatalf("unexpected %+v", c)
	}
}

func TestParseCapEff(t *testing.T) {
	n, ok := ParseCapEff("Name:\tx\nCapInh:\t0\nCapEff:\t00000000000000e0\n")
	if !ok || n != bothCaps {
		t.Fatalf("got %x %v", n, ok)
	}
	if _, ok := ParseCapEff("nothing"); ok {
		t.Fatal("expected !ok")
	}
}

func TestChildEnv(t *testing.T) {
	c := Config{Home: "/home/agent"}
	got := ChildEnv([]string{"A=1", "HOME=/home/node", "USER=node", "PATH=/bin"}, c)
	want := []string{"A=1", "PATH=/bin", "HOME=/home/agent", "USER=agent", "LOGNAME=agent"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestWrap(t *testing.T) {
	p, a := Wrap(Config{SetprivPath: "/usr/bin/setpriv"}, "/usr/bin/claude", []string{"claude", "-p", "x"})
	want := []string{"/usr/bin/setpriv", "--inh-caps=-all", "--ambient-caps=-all", "--", "/usr/bin/claude", "-p", "x"}
	if p != "/usr/bin/setpriv" || !slices.Equal(a, want) {
		t.Fatalf("got %s %v", p, a)
	}
	p, a = Wrap(Config{}, "/bin/ls", []string{"ls"})
	if p != "/bin/ls" || len(a) != 1 {
		t.Fatalf("no-wrapper mutated: %s %v", p, a)
	}
}

func TestApplySetsCredentialAndNoAmbient(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	apply(Config{Enabled: true, UID: 10001, GID: 1000, Home: "/home/agent"}, cmd)
	cr := cmd.SysProcAttr.Credential
	if cr == nil || cr.Uid != 10001 || cr.Gid != 1000 || !slices.Equal(cr.Groups, []uint32{1000}) {
		t.Fatalf("bad credential %+v", cr)
	}
	if len(cmd.SysProcAttr.AmbientCaps) != 0 {
		t.Fatal("ambient caps must be empty")
	}
}

func TestApplyDisabledIsNoop(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	Apply(cmd) // Current() is disabled in tests
	if cmd.SysProcAttr != nil {
		t.Fatal("expected no SysProcAttr")
	}
}

func TestParseProbe(t *testing.T) {
	ok := "10001\nName:\tcat\nCapInh:\t0000000000000000\nCapPrm:\t0000000000000000\nCapEff:\t0000000000000000\nCapBnd:\t00000000a80425fb\nCapAmb:\t0000000000000000\n"
	if err := ParseProbe(ok, 10001); err != nil {
		t.Fatal(err)
	}
	if err := ParseProbe(ok, 5); err == nil {
		t.Fatal("uid mismatch should fail")
	}
	leak := strings.Replace(ok, "CapAmb:\t0000000000000000", "CapAmb:\t00000000000000c0", 1)
	if err := ParseProbe(leak, 10001); err == nil || !strings.Contains(err.Error(), "CapAmb") {
		t.Fatalf("leak not detected: %v", err)
	}
	if err := ParseProbe("10001\nName: x\n", 10001); err == nil {
		t.Fatal("missing sets should fail")
	}
}

func TestShareGroupRW(t *testing.T) {
	home := t.TempDir()
	mk := func(rel string, mode os.FileMode, dir bool) string {
		p := filepath.Join(home, rel)
		if dir {
			if err := os.Mkdir(p, mode); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(p, []byte("{}"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil { // bypass umask
			t.Fatal(err)
		}
		return p
	}
	dir := mk(".claude", 0o700, true)
	settings := mk(".claude/settings.json", 0o600, false)
	creds := mk(".claude/.credentials.json", 0o640, false)
	global := mk(".claude.json", 0o664, false)
	if err := os.Symlink(settings, filepath.Join(home, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}

	shareGroupRW(home, claudeConfigPaths)

	for p, want := range map[string]os.FileMode{dir: 0o770, settings: 0o660, creds: 0o660, global: 0o664} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", p, got, want)
		}
	}
}

func TestShareClaudeConfigDisabledIsNoop(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ShareClaudeConfig(home) // Current() is disabled in tests
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %o", fi.Mode().Perm())
	}
}
