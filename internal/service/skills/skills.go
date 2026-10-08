// Package skills discovers Agent Skills (directories containing a SKILL.md file) on the local
// filesystem and provides safe, read-only access to their instructions and bundled files.
//
// A skill follows the Agent Skills format (https://agentskills.io/specification):
//
//	my-skill/
//	├── SKILL.md          # YAML frontmatter (name, description, ...) + markdown instructions
//	├── scripts/          # optional
//	├── references/       # optional
//	└── assets/           # optional
package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// SkillFileName is the name of the file that marks a directory as a skill.
	SkillFileName = "SKILL.md"

	// MaxFileSizeBytes is the largest skill file that can be read.
	MaxFileSizeBytes = 1 << 20

	// MaxListedFiles caps the number of bundled files reported for a single skill.
	MaxListedFiles = 500

	maxNameLength        = 64
	maxDescriptionLength = 1024
)

// ErrNotFound is returned when a skill or a file within a skill does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalidPath is returned when a requested file path is not allowed.
var ErrInvalidPath = errors.New("invalid path")

// Skill names become part of MCP tool arguments and prompt names, so they are restricted to the
// character set mandated by the Agent Skills specification.
var validSkillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill describes a single skill discovered on disk.
type Skill struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	License       string         `json:"license,omitempty"`
	Compatibility string         `json:"compatibility,omitempty"`
	AllowedTools  string         `json:"allowed_tools,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`

	// Dir is the absolute path of the skill directory. It is never exposed to MCP clients.
	Dir string `json:"-"`
}

type frontmatter struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	License       string         `yaml:"license"`
	Compatibility string         `yaml:"compatibility"`
	AllowedTools  string         `yaml:"allowed-tools"`
	Metadata      map[string]any `yaml:"metadata"`
}

// Store holds the set of skills loaded from one or more root directories.
type Store struct {
	skills map[string]*Skill
}

// Load discovers skills under the given root directories.
// A root may itself be a skill directory, or contain skill directories at any depth.
// Hidden directories below a root are skipped. Skills that fail validation are skipped with a
// warning; if two roots provide a skill with the same name, the first one wins.
func Load(roots []string) (*Store, error) {
	s := &Store{skills: make(map[string]*Skill)}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve skills directory %s: %w", root, err)
		}
		info, err := os.Stat(absRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to access skills directory %s: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("skills directory %s is not a directory", root)
		}
		if err := s.loadRoot(absRoot); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) loadRoot(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("[WARN] skills: skipping %s: %v", path, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, SkillFileName)); err != nil {
			return nil
		}

		skill, err := parseSkill(path)
		if err != nil {
			log.Printf("[WARN] skills: skipping skill at %s: %v", path, err)
			return fs.SkipDir
		}
		if existing, ok := s.skills[skill.Name]; ok {
			log.Printf(
				"[WARN] skills: skill %q at %s is shadowed by the one at %s", skill.Name, path, existing.Dir,
			)
			return fs.SkipDir
		}
		s.skills[skill.Name] = skill
		// a skill's own subdirectories (scripts/, references/, ...) are not separate skills
		return fs.SkipDir
	})
}

func parseSkill(dir string) (*Skill, error) {
	content, err := os.ReadFile(filepath.Join(dir, SkillFileName))
	if err != nil {
		return nil, err
	}
	rawFrontmatter, _, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}

	var fm frontmatter
	if err := yaml.Unmarshal(rawFrontmatter, &fm); err != nil {
		return nil, fmt.Errorf("invalid frontmatter in %s: %w", SkillFileName, err)
	}

	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	if len(name) > maxNameLength || !validSkillName.MatchString(name) {
		return nil, fmt.Errorf(
			"invalid skill name %q: must be at most %d lowercase letters, digits and single hyphens",
			name, maxNameLength,
		)
	}
	if name != filepath.Base(dir) {
		log.Printf("[WARN] skills: skill name %q does not match its directory name %s", name, filepath.Base(dir))
	}

	description := strings.TrimSpace(fm.Description)
	if description == "" {
		return nil, fmt.Errorf("skill %q has no description", name)
	}
	if len(description) > maxDescriptionLength {
		log.Printf("[WARN] skills: description of skill %q exceeds %d characters", name, maxDescriptionLength)
	}

	return &Skill{
		Name:          name,
		Description:   description,
		License:       strings.TrimSpace(fm.License),
		Compatibility: strings.TrimSpace(fm.Compatibility),
		AllowedTools:  strings.TrimSpace(fm.AllowedTools),
		Metadata:      fm.Metadata,
		Dir:           dir,
	}, nil
}

// splitFrontmatter separates the YAML frontmatter from the markdown body of a SKILL.md file.
func splitFrontmatter(content []byte) ([]byte, string, error) {
	text := strings.TrimPrefix(string(content), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, "", fmt.Errorf("%s must start with YAML frontmatter delimited by ---", SkillFileName)
	}
	rest := text[len("---\n"):]

	end := -1
	if strings.HasPrefix(rest, "---\n") || rest == "---" {
		end = 0
	} else if i := strings.Index(rest, "\n---\n"); i >= 0 {
		end = i + 1
	} else if strings.HasSuffix(rest, "\n---") {
		end = len(rest) - len("---")
	}
	if end < 0 {
		return nil, "", fmt.Errorf("%s frontmatter is not terminated by ---", SkillFileName)
	}

	fm := rest[:end]
	body := strings.TrimPrefix(rest[end:], "---")
	body = strings.TrimPrefix(body, "\n")
	return []byte(fm), body, nil
}

// List returns all loaded skills sorted by name.
func (s *Store) List() []Skill {
	out := make([]Skill, 0, len(s.skills))
	for _, sk := range s.skills {
		out = append(out, *sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the skill with the given name.
func (s *Store) Get(name string) (*Skill, error) {
	sk, ok := s.skills[name]
	if !ok {
		return nil, fmt.Errorf("skill %q: %w", name, ErrNotFound)
	}
	return sk, nil
}

// Instructions returns the markdown body of the skill's SKILL.md, without frontmatter.
// The file is re-read on every call so that edits are picked up without a restart.
func (sk *Skill) Instructions() (string, error) {
	content, err := sk.ReadFile(SkillFileName)
	if err != nil {
		return "", err
	}
	_, body, err := splitFrontmatter([]byte(content))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(body), nil
}

// Files returns the paths, relative to the skill directory and using forward slashes, of the
// regular files bundled with the skill (excluding SKILL.md and hidden entries).
// The second return value reports whether the list was truncated at MaxListedFiles.
func (sk *Skill) Files() ([]string, bool, error) {
	var files []string
	truncated := false
	err := filepath.WalkDir(sk.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == sk.Dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(sk.Dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == SkillFileName {
			return nil
		}
		if len(files) >= MaxListedFiles {
			truncated = true
			return fs.SkipAll
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(files)
	return files, truncated, nil
}

// ReadFile returns the UTF-8 text content of a file inside the skill directory.
// relPath must be relative to the skill directory; paths that escape it (including via
// symlinks), hidden files, non-regular files, binary files and files larger than
// MaxFileSizeBytes are rejected.
func (sk *Skill) ReadFile(relPath string) (string, error) {
	path, err := sk.resolve(relPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("file %q in skill %q: %w", relPath, sk.Name, ErrNotFound)
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file: %w", relPath, ErrInvalidPath)
	}
	if info.Size() > MaxFileSizeBytes {
		return "", fmt.Errorf("file %q is larger than %d bytes", relPath, MaxFileSizeBytes)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return "", fmt.Errorf("file %q is not a text file", relPath)
	}
	return string(content), nil
}

func (sk *Skill) resolve(relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", fmt.Errorf("path must not be empty: %w", ErrInvalidPath)
	}
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, `\`) {
		return "", fmt.Errorf("path %q must be relative to the skill directory: %w", relPath, ErrInvalidPath)
	}
	for _, part := range strings.FieldsFunc(relPath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." || strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("path %q must not contain hidden or parent components: %w", relPath, ErrInvalidPath)
		}
	}

	path := filepath.Join(sk.Dir, filepath.FromSlash(relPath))

	realDir, err := filepath.EvalSymlinks(sk.Dir)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("file %q in skill %q: %w", relPath, sk.Name, ErrNotFound)
		}
		return "", err
	}
	if realPath != realDir && !strings.HasPrefix(realPath, realDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q resolves outside the skill directory: %w", relPath, ErrInvalidPath)
	}
	return realPath, nil
}
