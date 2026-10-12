package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndEdit(t *testing.T) {
	store, _ := newInstallStore(t, testRepoArchive(t, "PDF v1"))
	changes := 0
	store.SetOnChange(func() { changes++ })

	created, err := store.Create(Content{
		Name:          "code-review",
		Description:   "Review pull requests.",
		License:       "MIT",
		Compatibility: "Claude, Cursor",
		Instructions:  "# Code review\n\nLook at the diff first.",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, changes)
	assert.Equal(t, "code-review", created.Name)
	assert.Equal(t, "Review pull requests.", created.Description)
	assert.Equal(t, "MIT", created.License)
	assert.Equal(t, "Claude, Cursor", created.Compatibility)
	assert.True(t, created.Removable)
	assert.Nil(t, created.Origin)
	assert.Equal(t, filepath.Join(store.InstallDir(), "code-review"), created.Dir)

	body, err := created.Instructions()
	require.NoError(t, err)
	assert.Equal(t, "# Code review\n\nLook at the diff first.", body)

	raw, err := os.ReadFile(filepath.Join(created.Dir, SkillFileName))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(raw), "---\n"))
	assert.Contains(t, string(raw), "name: code-review\n")

	_, err = store.Create(Content{Name: "code-review", Description: "duplicate"})
	assert.ErrorIs(t, err, apierrors.ErrConflict)

	edited, err := store.Edit("code-review", Content{
		Description:  "Review diffs carefully.",
		Instructions: "Focus on tests.",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, changes)
	assert.Equal(t, "Review diffs carefully.", edited.Description)
	assert.Empty(t, edited.License)
	body, err = edited.Instructions()
	require.NoError(t, err)
	assert.Equal(t, "Focus on tests.", body)
	assert.True(t, edited.Removable)
}

func TestCreate_Validation(t *testing.T) {
	store, _ := newInstallStore(t, testRepoArchive(t, "PDF v1"))

	_, err := store.Create(Content{Name: "Bad_Name", Description: "nope"})
	assert.ErrorIs(t, err, apierrors.ErrInvalidInput)

	_, err = store.Create(Content{Name: "ok", Description: ""})
	assert.ErrorIs(t, err, apierrors.ErrInvalidInput)

	_, err = store.Create(Content{Name: "ok", Description: strings.Repeat("x", maxDescriptionLength+1)})
	assert.ErrorIs(t, err, apierrors.ErrInvalidInput)
}

func TestCreate_Disabled(t *testing.T) {
	store, err := New(Options{Fetcher: &fakeFetcher{}})
	require.NoError(t, err)
	_, err = store.Create(Content{Name: "pdf", Description: "PDF files"})
	assert.ErrorIs(t, err, ErrInstallDisabled)
}

func TestCreate_DoesNotShadowSkillsDir(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "pdf"), "description: local pdf\n", "")
	store, _ := newInstallStore(t, testRepoArchive(t, "PDF v1"), dir)

	_, err := store.Create(Content{Name: "pdf", Description: "shadow"})
	assert.ErrorIs(t, err, apierrors.ErrConflict)
}

func TestEdit_PreservesBundledFilesAndOrigin(t *testing.T) {
	store, _ := newInstallStore(t, testRepoArchive(t, "PDF v1"))
	installed, err := store.Install(t.Context(), "acme/repo", []string{"pdf"}, false)
	require.NoError(t, err)
	require.Len(t, installed, 1)

	edited, err := store.Edit("pdf", Content{
		Description:  "Locally edited PDF skill.",
		License:      "Apache-2.0",
		Instructions: "Use the bundled script.",
	})
	require.NoError(t, err)
	assert.Equal(t, "Locally edited PDF skill.", edited.Description)
	assert.Equal(t, "Apache-2.0", edited.License)
	require.NotNil(t, edited.Origin)
	assert.Equal(t, "skills/pdf", edited.Origin.Path)

	files, _, err := edited.Files()
	require.NoError(t, err)
	assert.Equal(t, []string{"scripts/fill.py"}, files)

	_, err = store.Edit("missing", Content{Description: "nope"})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestEdit_SkillsDirSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "local-skill"), "description: original\n", "Old body.\n")
	store, err := New(Options{Dirs: []string{dir}})
	require.NoError(t, err)

	edited, err := store.Edit("local-skill", Content{
		Description:  "updated",
		Instructions: "New body.",
	})
	require.NoError(t, err)
	assert.Equal(t, "updated", edited.Description)
	assert.False(t, edited.Removable)
	body, err := edited.Instructions()
	require.NoError(t, err)
	assert.Equal(t, "New body.", body)
}

func TestEncodeSkillMD_RoundTrip(t *testing.T) {
	data, err := encodeSkillMD(frontmatter{
		Name:        "pdf",
		Description: "Handle: PDFs, forms, and scans.",
	}, "# PDF\n\nUse pypdf.")
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, SkillFileName), data, 0o644))
	sk, err := parseSkill(dir)
	require.NoError(t, err)
	assert.Equal(t, "pdf", sk.Name)
	assert.Equal(t, "Handle: PDFs, forms, and scans.", sk.Description)
	body, err := sk.Instructions()
	require.NoError(t, err)
	assert.Equal(t, "# PDF\n\nUse pypdf.", body)
}
