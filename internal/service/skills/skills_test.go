package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func writeSkill(t *testing.T, dir, frontmatter, body string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, SkillFileName), "---\n"+frontmatter+"---\n"+body)
}

func TestLoad_DiscoversNestedSkillsAndSkipsInvalidOnes(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "pdf"), "name: pdf\ndescription: Work with PDF files.\nlicense: MIT\n", "# PDF\n")
	writeSkill(t, filepath.Join(root, "team", "code-review"), "description: Review code.\n", "Review.\n")
	writeSkill(t, filepath.Join(root, "Bad_Name"), "description: invalid name.\n", "")
	writeSkill(t, filepath.Join(root, "no-desc"), "name: no-desc\n", "")
	writeSkill(t, filepath.Join(root, ".hidden"), "description: hidden.\n", "")
	writeFile(t, filepath.Join(root, "broken", SkillFileName), "no frontmatter here")
	// a SKILL.md inside a skill's subdirectory does not define a separate skill
	writeSkill(t, filepath.Join(root, "pdf", "nested"), "description: nested.\n", "")

	store, err := Load([]string{root})
	require.NoError(t, err)

	list := store.List()
	require.Len(t, list, 2)
	assert.Equal(t, "code-review", list[0].Name)
	assert.Equal(t, "Review code.", list[0].Description)
	assert.Equal(t, "pdf", list[1].Name)
	assert.Equal(t, "MIT", list[1].License)
}

func TestLoad_RootCanBeASkillAndFirstRootWins(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	skillRoot := filepath.Join(first, "pdf")
	writeSkill(t, skillRoot, "name: pdf\ndescription: first\n", "")
	writeSkill(t, filepath.Join(second, "pdf"), "name: pdf\ndescription: second\n", "")

	store, err := Load([]string{skillRoot, second})
	require.NoError(t, err)

	sk, err := store.Get("pdf")
	require.NoError(t, err)
	assert.Equal(t, "first", sk.Description)
}

func TestLoad_MissingDirectoryFails(t *testing.T) {
	_, err := Load([]string{filepath.Join(t.TempDir(), "missing")})
	assert.Error(t, err)
}

func TestSkill_InstructionsFilesAndReadFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	writeFile(t, filepath.Join(dir, SkillFileName), "\ufeff---\r\nname: pdf\r\ndescription: PDFs\r\n---\r\n\r\n# Use pypdf\r\n\r\n---\r\nmore\r\n")
	writeFile(t, filepath.Join(dir, "scripts", "fill.py"), "print('hi')\n")
	writeFile(t, filepath.Join(dir, "reference.md"), "ref\n")
	writeFile(t, filepath.Join(dir, ".secret"), "nope")
	writeFile(t, filepath.Join(dir, "bin.dat"), "a\x00b")
	writeFile(t, filepath.Join(root, "outside.txt"), "outside")
	require.NoError(t, os.Symlink(filepath.Join(root, "outside.txt"), filepath.Join(dir, "link.txt")))

	store, err := Load([]string{root})
	require.NoError(t, err)
	sk, err := store.Get("pdf")
	require.NoError(t, err)

	instructions, err := sk.Instructions()
	require.NoError(t, err)
	assert.Equal(t, "# Use pypdf\n\n---\nmore", instructions)

	files, truncated, err := sk.Files()
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, []string{"bin.dat", "reference.md", "scripts/fill.py"}, files)

	content, err := sk.ReadFile("scripts/fill.py")
	require.NoError(t, err)
	assert.Equal(t, "print('hi')\n", content)

	for _, p := range []string{"", "../outside.txt", "/etc/passwd", "scripts/../../outside.txt", ".secret", "link.txt"} {
		_, err := sk.ReadFile(p)
		assert.ErrorIs(t, err, ErrInvalidPath, "path %q", p)
	}
	_, err = sk.ReadFile("missing.md")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = sk.ReadFile("bin.dat")
	assert.Error(t, err)
	_, err = sk.ReadFile("scripts")
	assert.ErrorIs(t, err, ErrInvalidPath)
}

func TestStore_GetUnknown(t *testing.T) {
	store, err := Load(nil)
	require.NoError(t, err)
	_, err = store.Get("nope")
	assert.ErrorIs(t, err, ErrNotFound)
}
