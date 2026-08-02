// Package tempmail reads disposable inboxes from generator.email.
package tempmail

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	generatorBase = "https://generator.email"
	generatorUA   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	tokenTTL = 5 * time.Minute
)

// Message is one received disposable email.
type Message struct {
	ID          int64     `json:"id"`
	Address     string    `json:"address"`
	FromAddress string    `json:"from"`
	Subject     string    `json:"subject"`
	TextBody    string    `json:"text_body"`
	HTMLBody    string    `json:"html_body"`
	ReceivedAt  time.Time `json:"received_at"`
	hashID      string
}

// Generator is a client for generator.email. Call NewGenerator to construct it.
type Generator struct {
	doer interface {
		Do(*http.Request) (*http.Response, error)
	}
	mu       sync.Mutex
	token    string
	tokenAt  time.Time
	rng      *rand.Rand
	rngGuard sync.Mutex
}

func NewGenerator(doer interface {
	Do(*http.Request) (*http.Response, error)
}) *Generator {
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	return &Generator{
		doer: doer,
		rng:  rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

var tokenRe = regexp.MustCompile(`name="api-token"\s+content="([^"]+)"`)

func (g *Generator) apiToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	if g.token != "" && time.Since(g.tokenAt) < tokenTTL {
		token := g.token
		g.mu.Unlock()
		return token, nil
	}
	g.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, generatorBase+"/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", generatorUA)
	resp, err := g.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("generator.email unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	match := tokenRe.FindSubmatch(body)
	if match == nil {
		return "", fmt.Errorf("generator.email: no api-token on the page (the site may have changed)")
	}

	token := string(match[1])
	g.mu.Lock()
	g.token, g.tokenAt = token, time.Now()
	g.mu.Unlock()
	return token, nil
}

// Domains lists the shared domains offered by generator.email.
func (g *Generator) Domains(ctx context.Context) ([]string, error) {
	token, err := g.apiToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, generatorBase+"/api/domains.php", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", generatorUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", generatorBase+"/")
	req.Header.Set("X-Api-Token", token)
	resp, err := g.doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("generator.email domains: status %d", resp.StatusCode)
	}
	return parseDomains(body)
}

// Generate mints an address locally. Every address on the domain is already live.
func (g *Generator) Generate(_ context.Context, domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(domain, "@")))
	if domain == "" || strings.ContainsAny(domain, "@ /\\") {
		return "", fmt.Errorf("a valid domain is required")
	}
	return g.localPart() + "@" + domain, nil
}

// Messages reads an inbox and enriches the newest message with its HTML body.
func (g *Generator) Messages(ctx context.Context, address string) ([]Message, error) {
	local, domain, err := splitAddress(address)
	if err != nil {
		return nil, err
	}
	page, err := g.fetchInboxPage(ctx, local, domain)
	if err != nil {
		return nil, err
	}
	messages := parseInbox(page, address)
	if len(messages) == 0 {
		return messages, nil
	}
	if body := parseBody(page); body != "" {
		messages[0].HTMLBody = body
		return messages, nil
	}
	if messages[0].hashID != "" {
		messages[0].HTMLBody = g.fetchBody(ctx, local, domain, messages[0].hashID)
	}
	return messages, nil
}

// WaitForCode polls an inbox until a verification code appears or ctx expires.
func (g *Generator) WaitForCode(ctx context.Context, address string) (string, error) {
	local, domain, err := splitAddress(address)
	if err != nil {
		return "", err
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, fetchErr := g.fetchInboxPage(ctx, local, domain)
		if fetchErr == nil {
			for _, message := range parseInbox(page, address) {
				if code := ExtractCode(message); code != "" {
					return code, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func splitAddress(address string) (string, string, error) {
	local, domain, ok := strings.Cut(strings.TrimSpace(address), "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", "", fmt.Errorf("not an address: %q", address)
	}
	return local, domain, nil
}

func (g *Generator) fetchInboxPage(ctx context.Context, local, domain string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, generatorBase+"/inbox6/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", generatorUA)
	req.Header.Set("Referer", generatorBase+"/")
	req.Header.Set("Cookie", fmt.Sprintf(
		"embx=%%5B%%22%s%%40%s%%22%%5D; inbox_n=6; inbox_ctx=%s%%2F%s",
		url.QueryEscape(local), url.QueryEscape(domain), url.QueryEscape(domain), url.QueryEscape(local),
	))
	resp, err := g.doer.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("generator.email inbox: status %d", resp.StatusCode)
	}
	return string(body), nil
}

func (g *Generator) fetchBody(ctx context.Context, local, domain, hash string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, generatorBase+"/inbox1/", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", generatorUA)
	req.Header.Set("Referer", generatorBase+"/")
	req.Header.Set("Cookie", fmt.Sprintf(
		"embx=%%5B%%22%s%%40%s%%22%%5D; inbox_n=6; inbox_ctx=%s%%2F%s%%2F%s",
		url.QueryEscape(local), url.QueryEscape(domain), url.QueryEscape(domain), url.QueryEscape(local), url.QueryEscape(hash),
	))
	resp, err := g.doer.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ""
	}
	return parseBody(string(page))
}

func (g *Generator) localPart() string {
	first := []string{"adam", "bella", "caleb", "daniel", "elena", "felix", "grace", "henry", "iris", "jonas", "kara", "liam", "maya", "noah", "olive", "peter", "quinn", "rosa", "simon", "tessa", "uma", "victor", "wendy", "zane"}
	last := []string{"adams", "brooks", "carter", "dawson", "ellis", "fisher", "grant", "hayes", "ingram", "jensen", "keller", "lawson", "mercer", "nolan", "osborn", "parker", "reeves", "sawyer", "turner", "vaughn", "walsh"}
	g.rngGuard.Lock()
	defer g.rngGuard.Unlock()
	return fmt.Sprintf("%s%s%d", first[g.rng.Intn(len(first))], last[g.rng.Intn(len(last))], 100+g.rng.Intn(900))
}

const codeToken = `[A-Z0-9]{3,}(?:-[A-Z0-9]{3,})*|[0-9]{4,8}`

var codePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:verification|security|login|confirmation|one[- ]time)?\s*(?:code|otp|pin)\s*(?:is|:|=)?\s*(` + codeToken + `)\b`),
	regexp.MustCompile(`(?i)\b(` + codeToken + `)\s+is\s+your\s+(?:verification\s+)?(?:code|otp|pin)`),
	regexp.MustCompile(`(?i)^\s*(` + codeToken + `)\s*$`),
}

// ExtractCode pulls a verification code from the subject or text body.
func ExtractCode(message Message) string {
	for _, field := range []string{message.Subject, message.TextBody} {
		for _, pattern := range codePatterns {
			if match := pattern.FindStringSubmatch(field); len(match) > 1 {
				return match[1]
			}
		}
	}
	return ""
}

func parseDomains(body []byte) ([]string, error) {
	var raw []struct {
		ASCII string `json:"ascii"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, domain := range raw {
		if domain.ASCII != "" {
			out = append(out, domain.ASCII)
		}
	}
	return out, nil
}

var (
	fromRe      = regexp.MustCompile(`(?s)class="[^"]*from_div_45g45gg[^"]*">(.*?)</div>`)
	subjectRe   = regexp.MustCompile(`(?s)class="[^"]*subj_div_45g45gg[^"]*">(.*?)</div>`)
	timeRe      = regexp.MustCompile(`(?s)class="[^"]*time_div_45g45gg[^"]*">(.*?)</div>`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	hashRe      = regexp.MustCompile(`loadInboxClientSide\('[^'/]+/[^'/]+/([0-9a-f]{16,})'`)
	curMsgIDRe  = regexp.MustCompile(`cur_msg_id:"([0-9a-f]{16,})"`)
	bodyContent = regexp.MustCompile(`(?s)class="[^"]*mess_bodiyy[^"]*">(.*)`)
	bodyOpen    = regexp.MustCompile(`(?s)id="mail-summary-body"[^>]*>(.*)`)
	bodyCut     = regexp.MustCompile(`(?s)<(?:ins|script)\b`)
	metaRow     = regexp.MustCompile(`(?s)^.*?</button>`)
)

func parseInbox(page, address string) []Message {
	froms, subjects, times := captures(fromRe, page), captures(subjectRe, page), captures(timeRe, page)
	hashes := hashCaptures(page)
	n := min(len(froms), len(subjects))
	out := make([]Message, 0, n)
	for i := 0; i < n; i++ {
		if strings.EqualFold(froms[i], "From") && strings.EqualFold(subjects[i], "Subject") {
			continue
		}
		message := Message{ID: int64(len(out) + 1), Address: address, FromAddress: froms[i], Subject: subjects[i], TextBody: subjects[i]}
		if len(out) < len(hashes) {
			message.hashID = hashes[len(out)]
		}
		if i < len(times) {
			if received, err := time.Parse("2006-01-02 15:04:05", times[i]); err == nil {
				message.ReceivedAt = received.UTC()
			}
		}
		out = append(out, message)
	}
	if len(out) > 0 && out[0].hashID == "" {
		if match := curMsgIDRe.FindStringSubmatch(page); match != nil {
			out[0].hashID = match[1]
		}
	}
	return out
}

func hashCaptures(page string) []string {
	matches := hashRe.FindAllStringSubmatch(page, -1)
	out := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, match := range matches {
		if hash := match[1]; hash != "" && !seen[hash] {
			seen[hash] = true
			out = append(out, hash)
		}
	}
	return out
}

func parseBody(page string) string {
	if match := bodyContent.FindStringSubmatch(page); match != nil {
		inner := match[1]
		if cut := bodyCut.FindStringIndex(inner); cut != nil {
			inner = inner[:cut[0]]
		}
		if inner = strings.TrimSpace(inner); inner != "" {
			return inner
		}
	}
	match := bodyOpen.FindStringSubmatch(page)
	if match == nil {
		return ""
	}
	inner := match[1]
	if cut := bodyCut.FindStringIndex(inner); cut != nil {
		inner = inner[:cut[0]]
	}
	if row := metaRow.FindStringIndex(inner); row != nil {
		inner = inner[row[1]:]
	}
	return strings.TrimSpace(inner)
}

func captures(pattern *regexp.Regexp, value string) []string {
	matches := pattern.FindAllStringSubmatch(value, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(match[1], ""))))
	}
	return out
}
