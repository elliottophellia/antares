package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 30 * time.Second}

// SearchSkills finds skills matching a query. The bundled set always answers;
// remote sources are consulted only when the query names one, so browsing the
// hub never depends on the network.
func SearchSkills(ctx context.Context, query string) ([]Entry, error) {
	q := strings.TrimSpace(query)

	// A repository reference is a direct lookup, not a search.
	if ref, ok := parseGitHubRef(q); ok {
		found, err := searchGitHub(ctx, ref)
		if err != nil {
			return nil, err
		}
		return found, nil
	}
	if strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://") {
		e, err := fetchSkillURL(ctx, q)
		if err != nil {
			return nil, err
		}
		return []Entry{e}, nil
	}

	var out []Entry
	for _, e := range BundledSkills() {
		if matches(e, q) {
			e.Body = "" // the body is only needed at install time
			out = append(out, e)
		}
	}
	return out, nil
}

// InstallSkill writes a skill into dir and returns its path. The id is either
// "builtin/<name>", a GitHub reference, or a URL.
func InstallSkill(ctx context.Context, id, dir string) (Entry, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Entry{}, "", errors.New("which skill? pass an id from the catalogue, a github reference, or a URL")
	}

	var entry Entry
	switch {
	case strings.HasPrefix(id, "builtin/"):
		name := strings.TrimPrefix(id, "builtin/")
		found := false
		for _, e := range BundledSkills() {
			if e.Name == name {
				entry, found = e, true
				break
			}
		}
		if !found {
			return Entry{}, "", fmt.Errorf("no bundled skill named %q", name)
		}

	case strings.HasPrefix(id, "http://"), strings.HasPrefix(id, "https://"):
		e, err := fetchSkillURL(ctx, id)
		if err != nil {
			return Entry{}, "", err
		}
		entry = e

	default:
		ref, ok := parseGitHubRef(id)
		if !ok {
			return Entry{}, "", fmt.Errorf("%q is not a skill id, a github reference like owner/repo/path, or a URL", id)
		}
		found, err := searchGitHub(ctx, ref)
		if err != nil {
			return Entry{}, "", err
		}
		if len(found) == 0 {
			return Entry{}, "", fmt.Errorf("no SKILL.md found at %s", id)
		}
		if len(found) > 1 {
			names := make([]string, 0, len(found))
			for _, e := range found {
				names = append(names, e.ID)
			}
			return Entry{}, "", fmt.Errorf("that reference holds %d skills — install one of: %s",
				len(found), strings.Join(names, ", "))
		}
		entry = found[0]
	}

	if strings.TrimSpace(entry.Body) == "" {
		return Entry{}, "", fmt.Errorf("%s has no content", entry.ID)
	}
	if err := checkSkillSafety(entry.Body); err != nil {
		return Entry{}, "", err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Entry{}, "", err
	}
	path := filepath.Join(dir, SafeFileName(entry.Name)+".md")
	body := ensureFrontMatter(entry)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Entry{}, "", err
	}
	return entry, path, nil
}

// ensureFrontMatter guarantees the installed file carries a name, a
// description, and where it came from, even when the source omitted them.
func ensureFrontMatter(e Entry) string {
	text := strings.ReplaceAll(e.Body, "\r\n", "\n")
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			header := text[4 : 4+end]
			if !strings.Contains(header, "source:") {
				header += "\nsource: " + e.Source
			}
			if !strings.Contains(header, "name:") {
				header = "name: " + e.Name + "\n" + header
			}
			return "---\n" + header + text[4+end:]
		}
	}
	head := "---\nname: " + e.Name + "\n"
	if e.Summary != "" {
		head += "description: " + strings.ReplaceAll(e.Summary, "\n", " ") + "\n"
	}
	head += "source: " + e.Source + "\n---\n\n"
	return head + text
}

// dangerous patterns a skill has no business containing. A skill is prompt
// text that the model follows, so a file telling it to exfiltrate secrets is
// the whole attack — refusing at install is the only place to catch it.
var unsafeSkill = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)curl\s+[^\n|]*\|\s*(ba)?sh`), "pipes a download straight into a shell"},
	{regexp.MustCompile(`(?i)\brm\s+-rf\s+(/|~|\$HOME)(\s|$)`), "deletes a home or root directory"},
	{regexp.MustCompile(`(?i)(cat|read|send|upload|post)[^\n]{0,40}(\.ssh/id_|\.aws/credentials|\.env\b|api[_ ]?key)[^\n]{0,60}(http|curl|fetch|upload|post)`), "reads credentials and sends them somewhere"},
	{regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions`), "tries to override the system prompt"},
	{regexp.MustCompile(`(?i)\bdisregard\b[^\n]{0,40}\b(safety|guidelines|rules)\b`), "tries to disable safety rules"},
	{regexp.MustCompile(`(?i)chmod\s+777\s+/`), "makes the filesystem world-writable"},
}

// checkSkillSafety refuses a skill that reads as an attack.
func checkSkillSafety(body string) error {
	for _, rule := range unsafeSkill {
		if rule.re.MatchString(body) {
			return fmt.Errorf("refused: this skill %s. Read it yourself and add it by hand if you trust it", rule.why)
		}
	}
	return nil
}

// SafeFileName turns a skill name into a filename that cannot escape its
// directory or surprise a filesystem.
func SafeFileName(name string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ', r == '.', r == '/':
			return '-'
		}
		return -1
	}, name)
	out = strings.Trim(out, "-")
	if out == "" {
		return "skill"
	}
	return out
}

// ---- GitHub ------------------------------------------------------------------

type githubRef struct {
	Owner string
	Repo  string
	Path  string
}

var githubURL = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)(?:/(?:tree|blob)/[^/]+/(.*))?/?$`)
var shortRef = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)(?:/(.+))?$`)

// parseGitHubRef accepts "owner/repo", "owner/repo/path", or a github.com URL.
func parseGitHubRef(s string) (githubRef, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(s, "/"))
	if m := githubURL.FindStringSubmatch(s); m != nil {
		return githubRef{Owner: m[1], Repo: strings.TrimSuffix(m[2], ".git"), Path: m[3]}, true
	}
	// A bare owner/repo must not swallow "builtin/name" or a plain search.
	if strings.HasPrefix(s, "builtin/") || strings.Contains(s, " ") {
		return githubRef{}, false
	}
	if m := shortRef.FindStringSubmatch(s); m != nil {
		return githubRef{Owner: m[1], Repo: m[2], Path: m[3]}, true
	}
	return githubRef{}, false
}

type ghContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	DownloadURL string `json:"download_url"`
	HTMLURL     string `json:"html_url"`
}

// searchGitHub finds the skills a repository reference points at. It looks for
// a SKILL.md at the path, then one directory down, which covers both a repo of
// skills and a repo that is one skill.
func searchGitHub(ctx context.Context, ref githubRef) ([]Entry, error) {
	items, err := ghList(ctx, ref, ref.Path)
	if err != nil {
		return nil, err
	}

	var out []Entry
	var dirs []ghContent
	for _, it := range items {
		switch {
		case it.Type == "file" && isSkillFile(it.Name):
			e, err := fetchSkillContent(ctx, it, ref)
			if err == nil {
				out = append(out, e)
			}
		case it.Type == "dir":
			dirs = append(dirs, it)
		}
	}
	if len(out) > 0 {
		return out, nil
	}

	// Nothing at this level: look one level down, but not further — a deep
	// crawl of a large repository is a lot of requests for a guess.
	for i, d := range dirs {
		if i >= 40 {
			break
		}
		children, err := ghList(ctx, ref, d.Path)
		if err != nil {
			continue
		}
		for _, c := range children {
			if c.Type == "file" && isSkillFile(c.Name) {
				if e, err := fetchSkillContent(ctx, c, ref); err == nil {
					out = append(out, e)
				}
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no SKILL.md under %s/%s", ref.Owner, ref.Repo)
	}
	return out, nil
}

func isSkillFile(name string) bool {
	lower := strings.ToLower(name)
	return lower == "skill.md" || strings.HasSuffix(lower, ".skill.md")
}

func ghList(ctx context.Context, ref githubRef, path string) ([]ghContent, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s",
		ref.Owner, ref.Repo, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// An unauthenticated caller gets 60 requests an hour; a token in the
	// environment lifts that without any configuration of ours.
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s/%s has nothing at %q", ref.Owner, ref.Repo, path)
	case http.StatusForbidden:
		return nil, errors.New("GitHub is rate-limiting this machine — set GITHUB_TOKEN to raise the limit")
	default:
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	// The API returns an object for a file and an array for a directory.
	var list []ghContent
	if json.Unmarshal(body, &list) == nil {
		return list, nil
	}
	var one ghContent
	if err := json.Unmarshal(body, &one); err != nil {
		return nil, err
	}
	return []ghContent{one}, nil
}

func fetchSkillContent(ctx context.Context, it ghContent, ref githubRef) (Entry, error) {
	if it.DownloadURL == "" {
		return Entry{}, errors.New("no download url")
	}
	body, err := fetchText(ctx, it.DownloadURL)
	if err != nil {
		return Entry{}, err
	}
	meta := parseFrontMatter(body)
	name := meta.Name
	if name == "" {
		// A directory of skills names them by folder; SKILL.md itself does not.
		name = filepath.Base(filepath.Dir(it.Path))
		if name == "." || name == "/" || isSkillFile(name) {
			name = ref.Repo
		}
	}
	return Entry{
		ID:       fmt.Sprintf("%s/%s/%s", ref.Owner, ref.Repo, strings.TrimSuffix(it.Path, "/"+it.Name)),
		Kind:     "skill",
		Name:     name,
		Summary:  meta.Description,
		Source:   "github",
		Tags:     meta.Tags,
		Homepage: it.HTMLURL,
		Author:   ref.Owner,
		Body:     body,
	}, nil
}

func fetchSkillURL(ctx context.Context, url string) (Entry, error) {
	body, err := fetchText(ctx, url)
	if err != nil {
		return Entry{}, err
	}
	meta := parseFrontMatter(body)
	name := meta.Name
	if name == "" {
		base := filepath.Base(strings.SplitN(url, "?", 2)[0])
		name = strings.TrimSuffix(base, filepath.Ext(base))
		if isSkillFile(base) {
			name = filepath.Base(filepath.Dir(url))
		}
	}
	return Entry{
		ID:       url,
		Kind:     "skill",
		Name:     name,
		Summary:  meta.Description,
		Source:   "url",
		Tags:     meta.Tags,
		Homepage: url,
		Body:     body,
	}, nil
}

func fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", url, resp.Status)
	}
	// A skill is prose; anything enormous is not one.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
