package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"gopkg.in/yaml.v3"
)

// Content is the editable fields of a SKILL.md file.
type Content struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Instructions  string
}

// Create writes a new skill directory into the install directory and reloads the store.
func (s *Store) Create(content Content) (*Skill, error) {
	if s.installDir == "" {
		return nil, ErrInstallDisabled
	}

	name, description, err := validateWritableContent(content, true)
	if err != nil {
		return nil, err
	}
	content.Name = name
	content.Description = description

	data, err := encodeSkillMD(frontmatter{
		Name:          name,
		Description:   description,
		License:       strings.TrimSpace(content.License),
		Compatibility: strings.TrimSpace(content.Compatibility),
	}, content.Instructions)
	if err != nil {
		return nil, err
	}

	s.installMu.Lock()
	defer s.installMu.Unlock()

	if _, err := s.Get(name); err == nil {
		return nil, fmt.Errorf("skill %q already exists: %w", name, apierrors.ErrConflict)
	}

	dir := filepath.Join(s.installDir, name)
	if _, err := os.Lstat(dir); err == nil {
		return nil, fmt.Errorf("skill %q already exists: %w", name, apierrors.ErrConflict)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create skill %q: %w", name, err)
	}
	if err := writeFileAtomically(filepath.Join(dir, SkillFileName), data); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("failed to create skill %q: %w", name, err)
	}
	if err := s.reload(true); err != nil {
		return nil, err
	}
	return s.Get(name)
}

// Edit rewrites the SKILL.md of an existing skill and reloads the store.
// Bundled files and install origin metadata are left in place. The skill name cannot be changed.
func (s *Store) Edit(name string, content Content) (*Skill, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()

	sk, err := s.Get(name)
	if err != nil {
		return nil, err
	}

	content.Name = sk.Name
	_, description, err := validateWritableContent(content, false)
	if err != nil {
		return nil, err
	}

	data, err := encodeSkillMD(frontmatter{
		Name:          sk.Name,
		Description:   description,
		License:       strings.TrimSpace(content.License),
		Compatibility: strings.TrimSpace(content.Compatibility),
		AllowedTools:  sk.AllowedTools,
		Metadata:      sk.Metadata,
	}, content.Instructions)
	if err != nil {
		return nil, err
	}

	if err := writeFileAtomically(filepath.Join(sk.Dir, SkillFileName), data); err != nil {
		return nil, fmt.Errorf("failed to save skill %q: %w", name, err)
	}
	if err := s.reload(true); err != nil {
		return nil, err
	}
	return s.Get(name)
}

func validateWritableContent(content Content, requireName bool) (string, string, error) {
	name := strings.TrimSpace(content.Name)
	if requireName {
		if name == "" {
			return "", "", fmt.Errorf("skill name is required: %w", apierrors.ErrInvalidInput)
		}
		if len(name) > maxNameLength || !validSkillName.MatchString(name) {
			return "", "", fmt.Errorf(
				"invalid skill name %q: must be at most %d lowercase letters, digits and single hyphens: %w",
				name, maxNameLength, apierrors.ErrInvalidInput,
			)
		}
	}

	description := strings.TrimSpace(content.Description)
	if description == "" {
		return "", "", fmt.Errorf("skill description is required: %w", apierrors.ErrInvalidInput)
	}
	if len(description) > maxDescriptionLength {
		return "", "", fmt.Errorf(
			"skill description exceeds %d characters: %w", maxDescriptionLength, apierrors.ErrInvalidInput,
		)
	}
	return name, description, nil
}

func encodeSkillMD(fm frontmatter, body string) ([]byte, error) {
	raw, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("failed to encode skill frontmatter: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(raw)
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if body != "" {
		b.WriteString(body)
		b.WriteByte('\n')
	}

	data := []byte(b.String())
	if len(data) > MaxFileSizeBytes {
		return nil, fmt.Errorf("SKILL.md would exceed %d bytes: %w", MaxFileSizeBytes, apierrors.ErrInvalidInput)
	}
	return data, nil
}

func writeFileAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".skill-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
