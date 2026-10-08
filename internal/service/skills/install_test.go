package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func makeTarball(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: typeflag, Linkname: e.linkname}
		if typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

type fakeFetcher struct {
	archive []byte
	calls   []Source
}

func (f *fakeFetcher) Fetch(_ context.Context, src Source) (io.ReadCloser, error) {
	f.calls = append(f.calls, src)
	return io.NopCloser(bytes.NewReader(f.archive)), nil
}

func skillMD(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
}

func testRepoArchive(t *testing.T, pdfDescription string) []byte {
	return makeTarball(t, []tarEntry{
		{name: "repo-abc/", typeflag: tar.TypeDir},
		{name: "repo-abc/README.md", body: "readme"},
		{name: "repo-abc/skills/pdf/SKILL.md", body: skillMD("pdf", pdfDescription)},
		{name: "repo-abc/skills/pdf/scripts/fill.py", body: "print(1)"},
		{name: "repo-abc/skills/docx/SKILL.md", body: skillMD("docx", "Word documents")},
		{name: "repo-abc/.claude/skills/hidden-gem/SKILL.md", body: skillMD("hidden-gem", "Found in .claude")},
		{name: "repo-abc/skills/pdf/evil", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		{name: "repo-abc/../escape.txt", body: "nope"},
	})
}

func TestParseSource(t *testing.T) {
	cases := map[string]Source{
		"anthropics/skills":                                   {Owner: "anthropics", Repo: "skills"},
		"anthropics/skills@v1":                                {Owner: "anthropics", Repo: "skills", Ref: "v1"},
		"anthropics/skills/skills/pdf":                        {Owner: "anthropics", Repo: "skills", Path: "skills/pdf"},
		"https://github.com/a/b.git":                          {Owner: "a", Repo: "b"},
		"https://github.com/a/b/":                             {Owner: "a", Repo: "b"},
		"https://github.com/a/b/tree/main/skills/pdf":         {Owner: "a", Repo: "b", Ref: "main", Path: "skills/pdf"},
		"https://github.com/a/b/blob/dev/skills/pdf/SKILL.md": {Owner: "a", Repo: "b", Ref: "dev", Path: "skills/pdf"},
		"https://github.com/a/b/tree/HEAD":                    {Owner: "a", Repo: "b"},
	}
	for raw, want := range cases {
		got, err := ParseSource(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
		reparsed, err := ParseSource(got.String())
		require.NoError(t, err)
		assert.Equal(t, want, reparsed, "round trip of %s", raw)
	}

	for _, raw := range []string{
		"", "justone", "https://gitlab.com/a/b", "file:///etc", "a/b/../c", "https://github.com/a/b/issues/1", "a b/c",
	} {
		_, err := ParseSource(raw)
		assert.ErrorIs(t, err, apierrors.ErrInvalidInput, raw)
	}
}

func newInstallStore(t *testing.T, archive []byte, extraDirs ...string) (*Store, *fakeFetcher) {
	t.Helper()
	fetcher := &fakeFetcher{archive: archive}
	store, err := New(Options{
		Dirs:       extraDirs,
		InstallDir: filepath.Join(t.TempDir(), "installed"),
		Fetcher:    fetcher,
	})
	require.NoError(t, err)
	return store, fetcher
}

func TestPreviewAndInstall(t *testing.T) {
	store, fetcher := newInstallStore(t, testRepoArchive(t, "PDF v1"))
	changes := 0
	store.SetOnChange(func() { changes++ })

	preview, err := store.Preview(context.Background(), "acme/repo")
	require.NoError(t, err)
	assert.Equal(t, "acme/repo", preview.Source)
	require.Len(t, preview.Skills, 3)
	assert.Equal(t, Candidate{Name: "docx", Description: "Word documents", Path: "skills/docx"}, preview.Skills[0])
	assert.Equal(t, "hidden-gem", preview.Skills[1].Name)
	assert.Equal(t, "skills/pdf", preview.Skills[2].Path)
	assert.Empty(t, store.List(), "preview must not install anything")

	installed, err := store.Install(context.Background(), "acme/repo@main", []string{"pdf"}, false)
	require.NoError(t, err)
	require.Len(t, installed, 1)
	assert.Equal(t, 1, changes)
	assert.Equal(t, Source{Owner: "acme", Repo: "repo", Ref: "main"}, fetcher.calls[1])

	sk, err := store.Get("pdf")
	require.NoError(t, err)
	assert.True(t, sk.Removable)
	require.NotNil(t, sk.Origin)
	assert.Equal(t, "acme/repo", sk.Origin.Repo)
	assert.Equal(t, "main", sk.Origin.Ref)
	assert.Equal(t, "skills/pdf", sk.Origin.Path)
	assert.Equal(t, filepath.Join(store.InstallDir(), "pdf"), sk.Dir)

	files, _, err := sk.Files()
	require.NoError(t, err)
	assert.Equal(t, []string{"scripts/fill.py"}, files, "symlinks and the origin file must not be installed or listed")
	_, err = os.Stat(filepath.Join(filepath.Dir(store.InstallDir()), "escape.txt"))
	assert.True(t, os.IsNotExist(err))

	// installing again requires overwrite
	_, err = store.Install(context.Background(), "acme/repo", []string{"pdf"}, false)
	assert.ErrorIs(t, err, apierrors.ErrConflict)

	preview, err = store.Preview(context.Background(), "acme/repo")
	require.NoError(t, err)
	assert.True(t, preview.Skills[2].Installed)
	assert.Empty(t, preview.Skills[2].Conflict)

	_, err = store.Install(context.Background(), "acme/repo", []string{"missing"}, false)
	assert.ErrorIs(t, err, apierrors.ErrNotFound)

	entries, err := os.ReadDir(store.InstallDir())
	require.NoError(t, err)
	assert.Len(t, entries, 1, "temporary download directories must be cleaned up")
}

func TestInstallAllUpdateAndRemove(t *testing.T) {
	store, fetcher := newInstallStore(t, testRepoArchive(t, "PDF v1"))

	installed, err := store.Install(context.Background(), "https://github.com/acme/repo/tree/main/skills", nil, false)
	require.NoError(t, err)
	assert.Len(t, installed, 2, "only skills under the source path are installed")

	fetcher.archive = testRepoArchive(t, "PDF v2")
	updated, err := store.Update(context.Background(), "pdf")
	require.NoError(t, err)
	assert.Equal(t, "PDF v2", updated.Description)
	last := fetcher.calls[len(fetcher.calls)-1]
	assert.Equal(t, Source{Owner: "acme", Repo: "repo", Ref: "main", Path: "skills/pdf"}, last)

	require.NoError(t, store.Remove("pdf"))
	_, err = store.Get("pdf")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = os.Stat(filepath.Join(store.InstallDir(), "pdf"))
	assert.True(t, os.IsNotExist(err))
}

func TestInstall_DoesNotReplaceSkillsFromSkillsDirs(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, "pdf"), "description: local pdf\n", "")
	store, _ := newInstallStore(t, testRepoArchive(t, "PDF v1"), dir)

	preview, err := store.Preview(context.Background(), "acme/repo")
	require.NoError(t, err)
	assert.NotEmpty(t, preview.Skills[2].Conflict)

	_, err = store.Install(context.Background(), "acme/repo", []string{"pdf"}, true)
	assert.ErrorIs(t, err, apierrors.ErrConflict)

	err = store.Remove("pdf")
	assert.ErrorIs(t, err, apierrors.ErrInvalidInput)
}

func TestInstall_Disabled(t *testing.T) {
	store, err := New(Options{Fetcher: &fakeFetcher{}})
	require.NoError(t, err)
	_, err = store.Install(context.Background(), "acme/repo", nil, false)
	assert.ErrorIs(t, err, ErrInstallDisabled)
}

func TestReload_PicksUpManualChanges(t *testing.T) {
	dir := t.TempDir()
	store, err := New(Options{Dirs: []string{dir}})
	require.NoError(t, err)
	assert.Empty(t, store.List())

	writeSkill(t, filepath.Join(dir, "new-skill"), "description: added later\n", "")
	require.NoError(t, store.Reload())
	assert.Len(t, store.List(), 1)
}

func TestGitHubFetcher(t *testing.T) {
	archive := testRepoArchive(t, "PDF")
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if r.URL.Path == "/missing/repo/tar.gz/HEAD" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	f := &GitHubFetcher{Client: srv.Client(), CodeloadURL: srv.URL, APIURL: srv.URL}
	body, err := f.Fetch(context.Background(), Source{Owner: "acme", Repo: "repo"})
	require.NoError(t, err)
	_ = body.Close()
	assert.Equal(t, "/acme/repo/tar.gz/HEAD", gotPath)
	assert.Empty(t, gotAuth)

	f.Token = "secret"
	body, err = f.Fetch(context.Background(), Source{Owner: "acme", Repo: "repo", Ref: "v1"})
	require.NoError(t, err)
	_ = body.Close()
	assert.Equal(t, "/repos/acme/repo/tarball/v1", gotPath)
	assert.Equal(t, "Bearer secret", gotAuth)

	f.Token = ""
	_, err = f.Fetch(context.Background(), Source{Owner: "missing", Repo: "repo"})
	assert.ErrorIs(t, err, apierrors.ErrNotFound)
}
