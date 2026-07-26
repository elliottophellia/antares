package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/version"
)

var webClient = &http.Client{Timeout: 60 * time.Second}

// ---- web_fetch --------------------------------------------------------------

type webFetchTool struct{}

func (webFetchTool) Name() string { return "web_fetch" }
func (webFetchTool) Description() string {
	return "Fetch a URL and return its content as readable text (HTML is stripped to plain text)."
}
func (webFetchTool) Schema() map[string]any {
	return schema(map[string]any{
		"url":       prop("string", "Absolute http(s) URL to fetch."),
		"max_chars": propDefault("integer", "Maximum characters to return.", 20000),
		"raw":       propDefault("boolean", "Return the raw body instead of extracted text.", false),
	}, "url")
}

func (webFetchTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
		Raw      bool   `json:"raw"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	target := strings.TrimSpace(args.URL)
	if target == "" {
		return Errorf("url is required")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	u, err := url.Parse(target)
	if err != nil {
		return Errorf("invalid url: %v", err)
	}
	if args.MaxChars <= 0 || args.MaxChars > 200000 {
		args.MaxChars = 20000
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return Errorf("%v", err)
	}
	req.Header.Set("User-Agent", version.UserAgent()+" (+https://github.com/enowdev/antares)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.8")

	resp, err := webClient.Do(req)
	if err != nil {
		return Errorf("fetch failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Errorf("read body: %v", err)
	}
	if resp.StatusCode >= 400 {
		return Errorf("HTTP %d fetching %s\n%s", resp.StatusCode, u, truncateText(string(body), 1000))
	}

	ctype := resp.Header.Get("Content-Type")
	text := string(body)
	if !args.Raw && strings.Contains(ctype, "html") {
		text = htmlToText(text)
	}
	text = truncateText(text, args.MaxChars)
	header := fmt.Sprintf("Fetched %s (HTTP %d, %s)\n\n", u, resp.StatusCode, strings.SplitN(ctype, ";", 2)[0])
	return Result{Content: header + text, Meta: map[string]any{"url": u.String(), "status": resp.StatusCode}}
}

// Go's regexp engine (RE2) has no backreferences, so each container tag gets
// its own pattern rather than one alternation with \1.
var reContainers = func() []*regexp.Regexp {
	names := []string{"script", "style", "noscript", "svg", "head"}
	out := make([]*regexp.Regexp, 0, len(names))
	for _, n := range names {
		out = append(out, regexp.MustCompile(`(?is)<`+n+`[^>]*>.*?</\s*`+n+`\s*>`))
	}
	return out
}()

var (
	reTag      = regexp.MustCompile(`(?s)<[^>]+>`)
	reBlank    = regexp.MustCompile(`\n{3,}`)
	reSpaces   = regexp.MustCompile(`[ \t]{2,}`)
	reBlockEnd = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/h[1-6]|/tr)[^>]*>`)
)

// htmlToText strips markup down to readable prose.
func htmlToText(s string) string {
	for _, re := range reContainers {
		s = re.ReplaceAllString(s, " ")
	}
	s = reBlockEnd.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	s = reSpaces.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimSpace(l))
	}
	return strings.TrimSpace(reBlank.ReplaceAllString(strings.Join(out, "\n"), "\n\n"))
}

var htmlEntities = strings.NewReplacer(
	"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
	"&#39;", "'", "&apos;", "'", "&mdash;", "—", "&ndash;", "–", "&hellip;", "…",
	"&rsquo;", "'", "&lsquo;", "'", "&ldquo;", `"`, "&rdquo;", `"`,
)

func htmlUnescape(s string) string { return htmlEntities.Replace(s) }

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n\n… truncated (%d more characters)", len(s)-n)
}

// ---- web_search -------------------------------------------------------------

type webSearchTool struct{}

func (webSearchTool) Name() string { return "web_search" }
func (webSearchTool) Description() string {
	return "Search the web and return ranked results with titles, URLs, and snippets."
}
func (webSearchTool) Schema() map[string]any {
	return schema(map[string]any{
		"query":       prop("string", "Search query."),
		"max_results": propDefault("integer", "Number of results to return.", 8),
	}, "query")
}

func (webSearchTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return Errorf("query is required")
	}
	cfg := in.Deps.Config.Tools.WebSearch
	if args.MaxResults <= 0 {
		args.MaxResults = cfg.MaxResults
	}
	if args.MaxResults <= 0 || args.MaxResults > 25 {
		args.MaxResults = 8
	}

	var (
		results []searchResult
		err     error
	)
	switch strings.ToLower(cfg.Provider) {
	case "none", "off":
		return Errorf("web search is disabled (tools.web_search.provider = none)")
	case "brave":
		results, err = braveSearch(ctx, cfg.APIKey, query, args.MaxResults)
	case "tavily":
		results, err = tavilySearch(ctx, cfg.APIKey, query, args.MaxResults)
	case "searxng":
		results, err = searxngSearch(ctx, cfg.BaseURL, query, args.MaxResults)
	default:
		results, err = duckDuckGoSearch(ctx, query, args.MaxResults)
	}
	if err != nil {
		return Errorf("search failed: %v", err)
	}
	if len(results) == 0 {
		return Text(fmt.Sprintf("No results for %q", query))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d result(s) for %q:\n\n", len(results), query)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", truncateText(r.Snippet, 400))
		}
		b.WriteString("\n")
	}
	return Text(b.String())
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func getJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := webClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func braveSearch(ctx context.Context, apiKey, q string, n int) ([]searchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("brave search needs tools.web_search.api_key")
	}
	var raw struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	u := "https://api.search.brave.com/res/v1/web/search?count=" + fmt.Sprint(n) + "&q=" + url.QueryEscape(q)
	if err := getJSON(ctx, u, map[string]string{"X-Subscription-Token": apiKey, "Accept": "application/json"}, &raw); err != nil {
		return nil, err
	}
	out := make([]searchResult, 0, len(raw.Web.Results))
	for _, r := range raw.Web.Results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Snippet: htmlToText(r.Description)})
	}
	return out, nil
}

func tavilySearch(ctx context.Context, apiKey, q string, n int) ([]searchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("tavily search needs tools.web_search.api_key")
	}
	body, _ := json.Marshal(map[string]any{"api_key": apiKey, "query": q, "max_results": n})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := webClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]searchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

func searxngSearch(ctx context.Context, base, q string, n int) ([]searchResult, error) {
	if base == "" {
		return nil, fmt.Errorf("searxng needs tools.web_search.base_url")
	}
	var raw struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	u := strings.TrimRight(base, "/") + "/search?format=json&q=" + url.QueryEscape(q)
	if err := getJSON(ctx, u, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]searchResult, 0, n)
	for i, r := range raw.Results {
		if i >= n {
			break
		}
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

var reDDGResult = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>.*?class="result__snippet"[^>]*>(.*?)</a>`)

// duckDuckGoSearch scrapes the keyless HTML endpoint; it is the zero-config default.
func duckDuckGoSearch(ctx context.Context, q string, n int) ([]searchResult, error) {
	form := url.Values{"q": {q}}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; "+version.UserAgent()+")")

	resp, err := webClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	matches := reDDGResult.FindAllStringSubmatch(string(body), n)
	out := make([]searchResult, 0, len(matches))
	for _, m := range matches {
		link := m[1]
		// DuckDuckGo wraps results in a redirect carrying the real target in uddg.
		if parsed, err := url.Parse(link); err == nil {
			if real := parsed.Query().Get("uddg"); real != "" {
				link = real
			}
		}
		out = append(out, searchResult{
			Title:   strings.TrimSpace(htmlToText(m[2])),
			URL:     link,
			Snippet: strings.TrimSpace(htmlToText(m[3])),
		})
	}
	return out, nil
}
