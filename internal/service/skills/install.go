package skills

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
)

const (
	// originFileName records where an installed skill came from. It is hidden, so it is never
	// listed or readable through the MCP tools.
	originFileName = ".mcpjungle-skill.json"

	maxArchiveBytes = 200 << 20
	maxArchiveFiles = 20000
)

// ErrInstallDisabled is returned by install operations when no install directory is configured.
var ErrInstallDisabled = fmt.Errorf(
	"installing skills is disabled because no skills install directory is configured: %w", apierrors.ErrInvalidInput,
)

var (
	validGitHubName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	validGitRef     = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
)

// Origin records where an installed skill was downloaded from.
type Origin struct {
	// Source is the source string the skill can be reinstalled from.
	Source      string    `json:"source"`
	Repo        string    `json:"repo"`
	Ref         string    `json:"ref,omitempty"`
	Path        string    `json:"path,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

func readOrigin(dir string) *Origin {
	content, err := os.ReadFile(filepath.Join(dir, originFileName))
	if err != nil {
		return nil
	}
	var o Origin
	if err := json.Unmarshal(content, &o); err != nil {
		return nil
	}
	return &o
}

// Source identifies a directory in a GitHub repository to install skills from.
type Source struct {
	Owner string
	Repo  string
	// Ref is a branch, tag or commit. Empty means the default branch.
	Ref string
	// Path is a directory within the repository, using forward slashes. Empty means the root.
	Path string
}

// String returns the canonical form of the source, which ParseSource accepts.
func (s Source) String() string {
	if s.Ref == "" && s.Path == "" {
		return s.Owner + "/" + s.Repo
	}
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	u := "https://github.com/" + s.Owner + "/" + s.Repo + "/tree/" + ref
	if s.Path != "" {
		u += "/" + s.Path
	}
	return u
}

// ParseSource parses a skill source. Supported forms:
//
//	owner/repo
//	owner/repo/path/to/skills
//	owner/repo@ref
//	https://github.com/owner/repo
//	https://github.com/owner/repo/tree/<ref>/<path>
//	https://github.com/owner/repo/blob/<ref>/<path>/SKILL.md
func ParseSource(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	invalid := func(reason string) (Source, error) {
		return Source{}, fmt.Errorf("invalid skill source %q: %s: %w", raw, reason, apierrors.ErrInvalidInput)
	}
	if raw == "" {
		return invalid("must not be empty")
	}

	var src Source
	var segments []string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return invalid(err.Error())
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return invalid("only GitHub URLs are supported")
		}
		if host := strings.ToLower(u.Host); host != "github.com" && host != "www.github.com" {
			return invalid("only GitHub URLs are supported")
		}
		segments = splitPath(u.Path)
		if len(segments) < 2 {
			return invalid("expected https://github.com/<owner>/<repo>")
		}
		src.Owner, src.Repo = segments[0], strings.TrimSuffix(segments[1], ".git")
		rest := segments[2:]
		if len(rest) > 0 {
			if rest[0] != "tree" && rest[0] != "blob" {
				return invalid("expected a /tree/<ref>/<path> URL")
			}
			if len(rest) < 2 {
				return invalid("missing ref after /" + rest[0])
			}
			src.Ref = rest[1]
			src.Path = strings.Join(rest[2:], "/")
		}
	} else {
		spec := strings.TrimPrefix(raw, "github:")
		if i := strings.LastIndex(spec, "@"); i >= 0 {
			src.Ref = spec[i+1:]
			spec = spec[:i]
			if src.Ref == "" {
				return invalid("missing ref after @")
			}
		}
		segments = splitPath(spec)
		if len(segments) < 2 {
			return invalid("expected <owner>/<repo> or a GitHub URL")
		}
		src.Owner, src.Repo = segments[0], strings.TrimSuffix(segments[1], ".git")
		src.Path = strings.Join(segments[2:], "/")
	}

	if path.Base(src.Path) == SkillFileName {
		src.Path = path.Dir(src.Path)
		if src.Path == "." {
			src.Path = ""
		}
	}
	if src.Ref == "HEAD" {
		src.Ref = ""
	}

	if !validGitHubName.MatchString(src.Owner) || !validGitHubName.MatchString(src.Repo) {
		return invalid("owner and repository may only contain letters, digits, '.', '-' and '_'")
	}
	if src.Ref != "" && (!validGitRef.MatchString(src.Ref) || strings.Contains(src.Ref, "..")) {
		return invalid("invalid ref")
	}
	for _, part := range splitPath(src.Path) {
		if part == "." || part == ".." {
			return invalid("path must not contain . or .. segments")
		}
	}
	return src, nil
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
}

// Fetcher downloads the contents of a skill source as a gzipped tarball whose entries are
// nested under a single top-level directory, which is the format GitHub serves.
type Fetcher interface {
	Fetch(ctx context.Context, src Source) (io.ReadCloser, error)
}

// GitHubFetcher downloads repository tarballs from GitHub.
type GitHubFetcher struct {
	Client *http.Client
	// CodeloadURL is used for anonymous downloads.
	CodeloadURL string
	// APIURL is used when Token is set, which allows downloading private repositories.
	APIURL string
	Token  string
}

// NewGitHubFetcher returns a fetcher for github.com that authenticates with the GITHUB_TOKEN
// environment variable when it is set.
func NewGitHubFetcher() *GitHubFetcher {
	return &GitHubFetcher{
		Client:      &http.Client{Timeout: 2 * time.Minute},
		CodeloadURL: "https://codeload.github.com",
		APIURL:      "https://api.github.com",
		Token:       strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
	}
}

// Fetch implements Fetcher.
func (f *GitHubFetcher) Fetch(ctx context.Context, src Source) (io.ReadCloser, error) {
	ref := src.Ref
	var u string
	if f.Token != "" {
		u = fmt.Sprintf("%s/repos/%s/%s/tarball", f.APIURL, src.Owner, src.Repo)
		if ref != "" {
			u += "/" + url.PathEscape(ref)
		}
	} else {
		if ref == "" {
			ref = "HEAD"
		}
		u = fmt.Sprintf("%s/%s/%s/tar.gz/%s", f.CodeloadURL, src.Owner, src.Repo, url.PathEscape(ref))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if f.Token != "" {
		req.Header.Set("Authorization", "Bearer "+f.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s/%s: %w", src.Owner, src.Repo, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf(
				"repository %s/%s or ref %q not found (set GITHUB_TOKEN to install from private repositories): %w",
				src.Owner, src.Repo, src.Ref, apierrors.ErrNotFound,
			)
		}
		return nil, fmt.Errorf("failed to download %s/%s: GitHub returned %s", src.Owner, src.Repo, resp.Status)
	}
	return resp.Body, nil
}

// Candidate is a skill found in a source that can be installed.
type Candidate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Path is the skill directory within the repository.
	Path string `json:"path"`
	// Installed is true if a skill with this name is already installed; installing it again
	// requires overwrite.
	Installed bool `json:"installed"`
	// Conflict explains why the skill can't be installed, if it can't.
	Conflict string `json:"conflict,omitempty"`
}

// Preview lists the skills available in a source.
type Preview struct {
	Source string      `json:"source"`
	Skills []Candidate `json:"skills"`
}

type download struct {
	dir        string
	candidates map[string]*Skill
}

func (s *Store) download(ctx context.Context, src Source) (*download, error) {
	tmpParent := s.installDir
	if tmpParent == "" {
		tmpParent = os.TempDir()
	}
	dir, err := os.MkdirTemp(tmpParent, ".download-")
	if err != nil {
		return nil, err
	}

	body, err := s.fetcher.Fetch(ctx, src)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	defer body.Close()

	if err := extractTarball(body, dir, src.Path); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	root := filepath.Join(dir, filepath.FromSlash(src.Path))
	if _, err := os.Stat(root); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("path %q not found in %s/%s: %w", src.Path, src.Owner, src.Repo, apierrors.ErrNotFound)
	}
	candidates := make(map[string]*Skill)
	if err := discoverRemote(root, candidates); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if len(candidates) == 0 {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("no skills (directories with a %s) found in %s: %w", SkillFileName, src, apierrors.ErrNotFound)
	}
	return &download{dir: dir, candidates: candidates}, nil
}

// discoverRemote is like discover, but also searches hidden directories such as
// .claude/skills, which is a common location for skills in repositories.
func discoverRemote(root string, found map[string]*Skill) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if name := d.Name(); p != root && (name == ".git" || name == "node_modules") {
			return fs.SkipDir
		}
		if _, err := os.Stat(filepath.Join(p, SkillFileName)); err != nil {
			return nil
		}
		skill, err := parseSkill(p)
		if err != nil {
			return fs.SkipDir
		}
		if _, ok := found[skill.Name]; !ok {
			found[skill.Name] = skill
		}
		return fs.SkipDir
	})
}

func (d *download) relPath(sk *Skill) string {
	rel, err := filepath.Rel(d.dir, sk.Dir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (d *download) sortedNames() []string {
	names := make([]string, 0, len(d.candidates))
	for name := range d.candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Preview downloads a source and lists the skills it contains without installing anything.
func (s *Store) Preview(ctx context.Context, rawSource string) (*Preview, error) {
	src, err := ParseSource(rawSource)
	if err != nil {
		return nil, err
	}
	d, err := s.download(ctx, src)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(d.dir)

	p := &Preview{Source: src.String()}
	for _, name := range d.sortedNames() {
		sk := d.candidates[name]
		c := Candidate{Name: name, Description: sk.Description, Path: d.relPath(sk)}
		if existing, err := s.Get(name); err == nil {
			c.Installed = true
			if !existing.Removable {
				c.Conflict = "a skill with this name is provided by a skills directory and can't be replaced"
			}
		}
		p.Skills = append(p.Skills, c)
	}
	return p, nil
}

// Install downloads a source and installs the named skills from it into the install directory.
// If names is empty, every skill in the source is installed. Already installed skills are
// replaced only if overwrite is true.
func (s *Store) Install(ctx context.Context, rawSource string, names []string, overwrite bool) ([]Skill, error) {
	src, err := ParseSource(rawSource)
	if err != nil {
		return nil, err
	}
	return s.install(ctx, src, names, overwrite)
}

// Update reinstalls an installed skill from the source it was originally installed from.
func (s *Store) Update(ctx context.Context, name string) (*Skill, error) {
	sk, err := s.Get(name)
	if err != nil {
		return nil, err
	}
	if !sk.Removable || sk.Origin == nil {
		return nil, fmt.Errorf("skill %q was not installed from a remote source: %w", name, apierrors.ErrInvalidInput)
	}
	src, err := ParseSource(sk.Origin.Source)
	if err != nil {
		return nil, err
	}
	src.Path = sk.Origin.Path
	installed, err := s.install(ctx, src, []string{name}, true)
	if err != nil {
		return nil, err
	}
	return &installed[0], nil
}

func (s *Store) install(ctx context.Context, src Source, names []string, overwrite bool) ([]Skill, error) {
	if s.installDir == "" {
		return nil, ErrInstallDisabled
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()

	d, err := s.download(ctx, src)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(d.dir)

	if len(names) == 0 {
		names = d.sortedNames()
	}
	selected := make([]*Skill, 0, len(names))
	for _, name := range names {
		sk, ok := d.candidates[name]
		if !ok {
			return nil, fmt.Errorf("skill %q not found in %s: %w", name, src, apierrors.ErrNotFound)
		}
		if existing, err := s.Get(name); err == nil {
			if !existing.Removable {
				return nil, fmt.Errorf(
					"skill %q is provided by a skills directory and can't be replaced: %w", name, apierrors.ErrConflict,
				)
			}
			if !overwrite {
				return nil, fmt.Errorf("skill %q is already installed: %w", name, apierrors.ErrConflict)
			}
		}
		selected = append(selected, sk)
	}

	repoSource := Source{Owner: src.Owner, Repo: src.Repo, Ref: src.Ref}
	for _, sk := range selected {
		origin := Origin{
			Source:      repoSource.String(),
			Repo:        src.Owner + "/" + src.Repo,
			Ref:         src.Ref,
			Path:        d.relPath(sk),
			InstalledAt: time.Now().UTC(),
		}
		if err := s.place(sk, origin); err != nil {
			return nil, err
		}
	}

	if err := s.Reload(); err != nil {
		return nil, err
	}
	installed := make([]Skill, 0, len(selected))
	for _, sk := range selected {
		if got, err := s.Get(sk.Name); err == nil {
			installed = append(installed, *got)
		}
	}
	return installed, nil
}

// place moves a downloaded skill into the install directory, replacing any installed copy.
func (s *Store) place(sk *Skill, origin Origin) error {
	content, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sk.Dir, originFileName), content, 0o644); err != nil {
		return err
	}

	target := filepath.Join(s.installDir, sk.Name)
	var backup string
	if existing, err := s.Get(sk.Name); err == nil && existing.Removable {
		target = existing.Dir
	}
	if _, err := os.Lstat(target); err == nil {
		backup = filepath.Join(s.installDir, fmt.Sprintf(".replaced-%s-%d", sk.Name, time.Now().UnixNano()))
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("failed to replace skill %q: %w", sk.Name, err)
		}
	}
	if err := os.Rename(sk.Dir, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return fmt.Errorf("failed to install skill %q: %w", sk.Name, err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// Remove deletes an installed skill. Skills provided by skills directories can't be removed.
func (s *Store) Remove(name string) error {
	s.installMu.Lock()
	defer s.installMu.Unlock()

	sk, err := s.Get(name)
	if err != nil {
		return err
	}
	if !sk.Removable {
		return fmt.Errorf(
			"skill %q is provided by a skills directory; remove it from disk instead: %w", name, apierrors.ErrInvalidInput,
		)
	}
	if err := os.RemoveAll(sk.Dir); err != nil {
		return fmt.Errorf("failed to remove skill %q: %w", name, err)
	}
	return s.Reload()
}

// extractTarball extracts a GitHub-style tarball into dest, stripping the top-level directory.
// Only entries under subPath (if set) are extracted. Links and special files are skipped.
func extractTarball(r io.Reader, dest, subPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to read archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	files := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			continue
		}

		_, rel, ok := strings.Cut(strings.TrimPrefix(hdr.Name, "./"), "/")
		if !ok {
			continue
		}
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" {
			continue
		}
		parts := splitPath(rel)
		if slices.Contains(parts, "..") || strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) {
			continue
		}
		if subPath != "" && rel != subPath && !strings.HasPrefix(rel, subPath+"/") {
			continue
		}

		target := filepath.Join(dest, filepath.FromSlash(rel))
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}

		files++
		total += hdr.Size
		if files > maxArchiveFiles || total > maxArchiveBytes {
			return fmt.Errorf(
				"archive is too large (limit is %d files and %d MiB): %w",
				maxArchiveFiles, maxArchiveBytes>>20, apierrors.ErrInvalidInput,
			)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeArchiveFile(target, tr, hdr); err != nil {
			return err
		}
	}
}

func writeArchiveFile(target string, r io.Reader, hdr *tar.Header) error {
	mode := os.FileMode(0o644)
	if hdr.Mode&0o111 != 0 {
		mode = 0o755
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, hdr.Size); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to extract %s: %w", hdr.Name, err)
	}
	return f.Close()
}
