// Package skills implements the provider-neutral skills library: Agent Skills
// folders (a SKILL.md whose frontmatter has name + description, plus optional
// files) stored on disk, validated on the way in, and copied into a run's
// worktree in the directory each provider scans.
package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// MaxBundleBytes caps the total uncompressed size of a skill (and of an
	// uploaded zip).
	MaxBundleBytes = 1 << 20
	// MaxFiles caps the number of files in a skill.
	MaxFiles = 200
	// MaxNameLen / MaxDescriptionLen follow the Agent Skills spec.
	MaxNameLen        = 64
	MaxDescriptionLen = 1024

	skillFile = "SKILL.md"
)

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ErrInvalid wraps every validation failure so handlers can map it to a 400.
var ErrInvalid = errors.New("invalid skill")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Meta is the parsed SKILL.md frontmatter.
type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ValidName reports whether name is a legal skill slug.
func ValidName(name string) bool {
	return len(name) <= MaxNameLen && slugRE.MatchString(name)
}

// ParseFrontmatter extracts and validates the YAML frontmatter of a SKILL.md.
func ParseFrontmatter(md []byte) (Meta, error) {
	s := strings.TrimPrefix(string(md), "\xef\xbb\xbf")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return Meta{}, invalid("SKILL.md must start with a '---' frontmatter block")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Meta{}, invalid("SKILL.md frontmatter is not closed with '---'")
	}
	var m Meta
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return Meta{}, invalid("SKILL.md frontmatter is not valid YAML: %v", err)
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	if m.Name == "" {
		return Meta{}, invalid("frontmatter 'name' is required")
	}
	if !ValidName(m.Name) {
		return Meta{}, invalid("frontmatter 'name' must be a lowercase slug (a-z, 0-9, single hyphens, max %d chars)", MaxNameLen)
	}
	if m.Description == "" {
		return Meta{}, invalid("frontmatter 'description' is required")
	}
	if len(m.Description) > MaxDescriptionLen {
		return Meta{}, invalid("frontmatter 'description' must be at most %d characters", MaxDescriptionLen)
	}
	return m, nil
}

// Bundle is a validated skill ready to be written to disk.
type Bundle struct {
	Meta
	// Files maps slash-separated relative paths to contents; always has SKILL.md.
	Files map[string][]byte
}

// BundleFromSkillMD builds a single-file bundle from a SKILL.md body.
func BundleFromSkillMD(md []byte) (*Bundle, error) {
	if len(md) > MaxBundleBytes {
		return nil, invalid("SKILL.md exceeds %d bytes", MaxBundleBytes)
	}
	m, err := ParseFrontmatter(md)
	if err != nil {
		return nil, err
	}
	return &Bundle{Meta: m, Files: map[string][]byte{skillFile: md}}, nil
}

// cleanEntry validates a zip entry name and returns its normalized form.
func cleanEntry(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") {
		return "", invalid("illegal path %q in zip", name)
	}
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return "", invalid("absolute path %q in zip", name)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if seg == ".." {
			return "", invalid("path traversal in zip entry %q", name)
		}
	}
	return path.Clean(name), nil
}

// BundleFromZip builds a bundle from a zip of a skill folder. SKILL.md may be
// at the zip root or inside a single top-level folder. Path traversal,
// absolute paths, symlinks and oversized archives are rejected.
func BundleFromZip(data []byte) (*Bundle, error) {
	if len(data) > MaxBundleBytes {
		return nil, invalid("zip exceeds %d bytes", MaxBundleBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("not a valid zip: %v", err)
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		clean, err := cleanEntry(f.Name)
		if err != nil {
			return nil, err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, invalid("symlink %q in zip", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, invalid("non-regular file %q in zip", f.Name)
		}
		// Skip macOS resource-fork noise.
		if strings.HasPrefix(clean, "__MACOSX/") {
			continue
		}
		if len(files) >= MaxFiles {
			return nil, invalid("zip has more than %d files", MaxFiles)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, invalid("read %q: %v", f.Name, err)
		}
		// Never trust the declared size: cap the actual decompressed bytes.
		b, err := io.ReadAll(io.LimitReader(rc, MaxBundleBytes-total+1))
		_ = rc.Close()
		if err != nil {
			return nil, invalid("read %q: %v", f.Name, err)
		}
		total += int64(len(b))
		if total > MaxBundleBytes {
			return nil, invalid("zip contents exceed %d bytes uncompressed", MaxBundleBytes)
		}
		if _, dup := files[clean]; dup {
			return nil, invalid("duplicate entry %q in zip", f.Name)
		}
		files[clean] = b
	}
	// Strip a single wrapping folder.
	if _, ok := files[skillFile]; !ok {
		prefix := ""
		for p := range files {
			top, _, found := strings.Cut(p, "/")
			if !found {
				return nil, invalid("zip must contain %s at its root or in a single top-level folder", skillFile)
			}
			if prefix == "" {
				prefix = top
			} else if prefix != top {
				return nil, invalid("zip must contain %s at its root or in a single top-level folder", skillFile)
			}
		}
		stripped := map[string][]byte{}
		for p, b := range files {
			stripped[strings.TrimPrefix(p, prefix+"/")] = b
		}
		files = stripped
	}
	md, ok := files[skillFile]
	if !ok {
		return nil, invalid("zip has no %s", skillFile)
	}
	m, err := ParseFrontmatter(md)
	if err != nil {
		return nil, err
	}
	return &Bundle{Meta: m, Files: files}, nil
}

// Store keeps skill folders under Dir/<name>/.
type Store struct{ Dir string }

// Path returns the on-disk folder of the named skill.
func (s Store) Path(name string) string { return filepath.Join(s.Dir, name) }

// Write stores the bundle, replacing any existing folder of that name. The
// swap is staged in a temp dir so a failed write leaves the old copy intact.
func (s Store) Write(b *Bundle) error {
	if !ValidName(b.Name) {
		return invalid("bad skill name %q", b.Name)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.Dir, ".tmp-"+b.Name+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for rel, data := range b.Files {
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	final := s.Path(b.Name)
	old := final + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Rename(old, final)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// Remove deletes the named skill's folder.
func (s Store) Remove(name string) error {
	if !ValidName(name) {
		return nil
	}
	return os.RemoveAll(s.Path(name))
}

// ReadFiles returns the relative paths of a stored skill's files, sorted.
func (s Store) ReadFiles(name string) ([]string, error) {
	var out []string
	root := s.Path(name)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
