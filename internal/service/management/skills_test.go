package management

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/internal/service/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFetcher struct {
	archive []byte
}

func (f *fakeFetcher) Fetch(_ context.Context, _ skills.Source) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.archive)), nil
}

func skillMD(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
}

// repoArchive builds a GitHub-style tarball containing the skills "pdf" and "docx".
func repoArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"repo-abc/skills/pdf/SKILL.md":  skillMD("pdf", "PDF files"),
		"repo-abc/skills/docx/SKILL.md": skillMD("docx", "Word documents"),
	} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// newSkillStore returns a store with a read-only skills directory holding "local-skill",
// and, if installDir is true, an install directory backed by repoArchive.
func newSkillStore(t *testing.T, installDir bool) *skills.Store {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "local-skill"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "local-skill", skills.SkillFileName), []byte(skillMD("local-skill", "Local")), 0o644,
	))
	opts := skills.Options{Dirs: []string{dir}, Fetcher: &fakeFetcher{archive: repoArchive(t)}}
	if installDir {
		opts.InstallDir = filepath.Join(t.TempDir(), "installed")
	}
	store, err := skills.New(opts)
	require.NoError(t, err)
	return store
}

func skillNames(t *testing.T, f *fixture) []string {
	t.Helper()
	var out struct {
		Skills []skillSummary `json:"skills"`
	}
	require.Equal(t, "", f.call(t, devCtx(), listSkillsToolName, nil, &out))
	names := make([]string, len(out.Skills))
	for i, sk := range out.Skills {
		names[i] = sk.Name
	}
	return names
}

func TestSkillToolsDependOnSkillsConfiguration(t *testing.T) {
	skillTools := []string{
		listSkillsToolName, reloadSkillsToolName, previewSkillsToolName,
		installSkillsToolName, updateSkillToolName, removeSkillToolName,
	}
	present := func(f *fixture) []bool {
		names := f.listToolNames(t, devCtx())
		out := make([]bool, len(skillTools))
		for i, tool := range skillTools {
			out[i] = contains(names, toolName(tool))
		}
		return out
	}

	assert.Equal(t, []bool{false, false, false, false, false, false}, present(newFixture(t)))
	assert.Equal(t, []bool{true, true, false, false, false, false}, present(newFixtureWithSkills(t, newSkillStore(t, false))))
	assert.Equal(t, []bool{true, true, true, true, true, true}, present(newFixtureWithSkills(t, newSkillStore(t, true))))
}

func TestSkillLifecycle(t *testing.T) {
	f := newFixtureWithSkills(t, newSkillStore(t, true))
	ctx := devCtx()
	assert.Equal(t, []string{"local-skill"}, skillNames(t, f))

	var preview skills.Preview
	require.Equal(t, "", f.call(t, ctx, previewSkillsToolName, map[string]any{"source": "acme/skills"}, &preview))
	require.Len(t, preview.Skills, 2)
	assert.Equal(t, "docx", preview.Skills[0].Name)
	assert.False(t, preview.Skills[0].Installed)

	var installed struct {
		Installed []skillSummary `json:"installed"`
	}
	require.Equal(t, "", f.call(t, ctx, installSkillsToolName, map[string]any{
		"source": "acme/skills", "skills": []string{"pdf"},
	}, &installed))
	require.Len(t, installed.Installed, 1)
	assert.True(t, installed.Installed[0].Removable)
	assert.Equal(t, "acme/skills", installed.Installed[0].Origin.Source)
	assert.Equal(t, []string{"local-skill", "pdf"}, skillNames(t, f))

	// installed skills are served right away by the skills server
	msg := f.rpc(t, ctx, "prompts/list", map[string]any{})
	var prompts struct {
		Prompts []struct {
			Name string `json:"name"`
		} `json:"prompts"`
	}
	decodeResult(t, msg, &prompts)
	var promptNames []string
	for _, p := range prompts.Prompts {
		promptNames = append(promptNames, p.Name)
	}
	assert.Contains(t, promptNames, mcp.SkillsServerName+"__pdf")

	assert.Contains(t,
		f.call(t, ctx, installSkillsToolName, map[string]any{"source": "acme/skills", "skills": []string{"pdf"}}, nil),
		"already installed",
	)
	require.Equal(t, "", f.call(t, ctx, installSkillsToolName, map[string]any{
		"source": "acme/skills", "skills": []string{"pdf"}, "overwrite": true,
	}, nil))

	var updated skillSummary
	require.Equal(t, "", f.call(t, ctx, updateSkillToolName, map[string]any{"name": "pdf"}, &updated))
	assert.Equal(t, "pdf", updated.Name)
	assert.Contains(t, f.call(t, ctx, updateSkillToolName, map[string]any{"name": "local-skill"}, nil), "not installed from a remote source")

	assert.Contains(t, f.call(t, ctx, removeSkillToolName, map[string]any{"name": "local-skill"}, nil), "skills directory")
	require.Equal(t, "", f.call(t, ctx, removeSkillToolName, map[string]any{"name": "pdf"}, nil))
	assert.Equal(t, []string{"local-skill"}, skillNames(t, f))
	assert.Contains(t, f.call(t, ctx, removeSkillToolName, map[string]any{"name": "pdf"}, nil), "not found")
}

func TestReloadSkillsPicksUpChangesOnDisk(t *testing.T) {
	store := newSkillStore(t, true)
	f := newFixtureWithSkills(t, store)

	dir := filepath.Join(store.InstallDir(), "manual")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, skills.SkillFileName), []byte(skillMD("manual", "Added by hand")), 0o644))

	var out struct {
		SkillCount int `json:"skill_count"`
	}
	require.Equal(t, "", f.call(t, devCtx(), reloadSkillsToolName, nil, &out))
	assert.Equal(t, 2, out.SkillCount)
	assert.Contains(t, skillNames(t, f), "manual")
}
