package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// This file implements antares' native open-source-intelligence (OSINT) toolset:
// self-contained reconnaissance lookups the agent can call directly. They gather
// only publicly available information and are meant for authorized security
// assessments, investigations, and footprint reviews.

// ---- osint_dns --------------------------------------------------------------

type osintDNSTool struct{}

func (osintDNSTool) Name() string { return "osint_dns" }
func (osintDNSTool) Description() string {
	return "Enumerate a domain's DNS records (A, AAAA, MX, NS, TXT, CNAME) and audit its email " +
		"security posture: SPF presence and strength, DMARC policy, and DKIM selectors. No API key needed."
}
func (osintDNSTool) Schema() map[string]any {
	return schema(map[string]any{
		"domain": prop("string", "The domain to enumerate, e.g. example.com."),
	}, "domain")
}
func (osintDNSTool) RequiresApproval() bool { return false }

var dkimSelectors = []string{"default", "google", "mail", "dkim", "s1", "s2", "selector1", "selector2", "k1"}

func (osintDNSTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Domain string `json:"domain"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	domain := strings.TrimSpace(strings.ToLower(args.Domain))
	if domain == "" {
		return Errorf("domain is required")
	}
	r := &net.Resolver{}
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var b strings.Builder
	fmt.Fprintf(&b, "DNS intelligence for %s\n\n", domain)

	if ips, err := r.LookupHost(tctx, domain); err == nil && len(ips) > 0 {
		fmt.Fprintf(&b, "A/AAAA: %s\n", strings.Join(ips, ", "))
	}
	if mx, err := r.LookupMX(tctx, domain); err == nil && len(mx) > 0 {
		hosts := make([]string, 0, len(mx))
		for _, m := range mx {
			hosts = append(hosts, fmt.Sprintf("%s (pref %d)", strings.TrimSuffix(m.Host, "."), m.Pref))
		}
		fmt.Fprintf(&b, "MX: %s\n", strings.Join(hosts, ", "))
	}
	if ns, err := r.LookupNS(tctx, domain); err == nil && len(ns) > 0 {
		hosts := make([]string, 0, len(ns))
		for _, n := range ns {
			hosts = append(hosts, strings.TrimSuffix(n.Host, "."))
		}
		fmt.Fprintf(&b, "NS: %s\n", strings.Join(hosts, ", "))
	}
	if cname, err := r.LookupCNAME(tctx, domain); err == nil && cname != "" && strings.TrimSuffix(cname, ".") != domain {
		fmt.Fprintf(&b, "CNAME: %s\n", strings.TrimSuffix(cname, "."))
	}

	txt, _ := r.LookupTXT(tctx, domain)
	if len(txt) > 0 {
		fmt.Fprintf(&b, "TXT: %s\n", strings.Join(txt, " | "))
	}

	// Email security audit.
	b.WriteString("\nEmail security:\n")
	spf := ""
	for _, t := range txt {
		if strings.HasPrefix(strings.ToLower(t), "v=spf1") {
			spf = t
			break
		}
	}
	switch {
	case spf == "":
		b.WriteString("- SPF: MISSING (domain can be spoofed)\n")
	case strings.Contains(spf, "+all"):
		b.WriteString("- SPF: present but +all — anyone may send (dangerous)\n")
	case strings.Contains(spf, "~all"):
		b.WriteString("- SPF: present but ~all — soft-fail only (weak)\n")
	default:
		b.WriteString("- SPF: present\n")
	}
	if dmarc, err := r.LookupTXT(tctx, "_dmarc."+domain); err == nil && len(dmarc) > 0 {
		policy := "none"
		for _, d := range dmarc {
			if i := strings.Index(strings.ToLower(d), "p="); i >= 0 {
				policy = strings.TrimSpace(strings.SplitN(d[i+2:], ";", 2)[0])
			}
		}
		fmt.Fprintf(&b, "- DMARC: present (p=%s)%s\n", policy, ternary(policy == "none", " — not enforced", ""))
	} else {
		b.WriteString("- DMARC: MISSING\n")
	}
	found := []string{}
	for _, sel := range dkimSelectors {
		if rec, err := r.LookupTXT(tctx, sel+"._domainkey."+domain); err == nil && len(rec) > 0 {
			found = append(found, sel)
		}
	}
	if len(found) > 0 {
		fmt.Fprintf(&b, "- DKIM: selectors found: %s\n", strings.Join(found, ", "))
	} else {
		b.WriteString("- DKIM: no common selectors found\n")
	}
	return Text(b.String())
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ---- osint_dorks ------------------------------------------------------------

type osintDorksTool struct{}

func (osintDorksTool) Name() string { return "osint_dorks" }
func (osintDorksTool) Description() string {
	return "Generate targeted Google search (dork) URLs for a name, email, username, or domain — " +
		"profiles, documents, and exposure across common sites. Returns URLs to open or feed to a fetch tool; runs no requests itself."
}
func (osintDorksTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string", "The name, email, username, or domain to build dorks for."),
	}, "target")
}
func (osintDorksTool) RequiresApproval() bool { return false }

var dorkTemplates = []string{
	`"%[1]s"`,
	`"%[1]s" site:linkedin.com`,
	`"%[1]s" site:twitter.com`,
	`"%[1]s" site:facebook.com`,
	`"%[1]s" site:instagram.com`,
	`"%[1]s" site:github.com`,
	`"%[1]s" filetype:pdf`,
	`"%[1]s" inurl:profile`,
	`"%[1]s" resume OR cv`,
	`"%[1]s" leaked OR breach OR dump`,
	`intitle:"%[1]s"`,
	`"%[1]s" -site:linkedin.com -site:facebook.com`,
}

func (osintDorksTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Target string `json:"target"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return Errorf("target is required")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Google dorks for %q:\n\n", target)
	for _, tmpl := range dorkTemplates {
		q := fmt.Sprintf(tmpl, target)
		fmt.Fprintf(&b, "- %s\n  https://www.google.com/search?q=%s\n", q, url.QueryEscape(q))
	}
	return Text(b.String())
}

// ---- osint_whois ------------------------------------------------------------

type osintWhoisTool struct{}

func (osintWhoisTool) Name() string { return "osint_whois" }
func (osintWhoisTool) Description() string {
	return "Look up WHOIS registration for a domain — registrar, creation/expiry dates, name servers, " +
		"and status — by querying the authoritative WHOIS server over port 43. No API key needed."
}
func (osintWhoisTool) Schema() map[string]any {
	return schema(map[string]any{
		"domain": prop("string", "The domain to look up, e.g. example.com."),
	}, "domain")
}
func (osintWhoisTool) RequiresApproval() bool { return false }

func (osintWhoisTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Domain string `json:"domain"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	domain := strings.TrimSpace(strings.ToLower(args.Domain))
	if domain == "" {
		return Errorf("domain is required")
	}
	// Ask IANA for the authoritative WHOIS server, then query it.
	root, err := whoisQuery(ctx, "whois.iana.org:43", domain)
	if err != nil {
		return Errorf("whois lookup failed: %v", err)
	}
	server := ""
	for _, line := range strings.Split(root, "\n") {
		if v, ok := cutPrefixFold(line, "refer:"); ok {
			server = strings.TrimSpace(v)
			break
		}
		if v, ok := cutPrefixFold(line, "whois:"); ok {
			server = strings.TrimSpace(v)
			break
		}
	}
	if server == "" {
		return Text(fmt.Sprintf("WHOIS for %s (via IANA):\n\n%s", domain, whoisTrim(root)))
	}
	body, err := whoisQuery(ctx, server+":43", domain)
	if err != nil || strings.TrimSpace(body) == "" {
		return Text(fmt.Sprintf("WHOIS for %s (via IANA):\n\n%s", domain, whoisTrim(root)))
	}
	return Text(fmt.Sprintf("WHOIS for %s (via %s):\n\n%s", domain, server, whoisTrim(body)))
}

func whoisQuery(ctx context.Context, addr, query string) (string, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write([]byte(query + "\r\n")); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(conn, 1<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// whoisTrim keeps the response readable: drops boilerplate comment lines.
func whoisTrim(s string) string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "%") || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(t), "notice:") || strings.HasPrefix(strings.ToLower(t), "terms of use") {
			continue
		}
		out = append(out, line)
		if len(out) > 120 {
			break
		}
	}
	return strings.Join(out, "\n")
}

func cutPrefixFold(line, prefix string) (string, bool) {
	if len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
		return line[len(prefix):], true
	}
	return "", false
}

// ---- osint_ip ---------------------------------------------------------------

type osintIPTool struct{}

func (osintIPTool) Name() string { return "osint_ip" }
func (osintIPTool) Description() string {
	return "Geolocate and profile an IP address: country, region, city, ISP, organization, ASN, and " +
		"reverse DNS. Uses a keyless public geolocation service."
}
func (osintIPTool) Schema() map[string]any {
	return schema(map[string]any{
		"ip": prop("string", "The IPv4 or IPv6 address to look up."),
	}, "ip")
}
func (osintIPTool) RequiresApproval() bool { return false }

func (osintIPTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		IP string `json:"ip"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	ip := strings.TrimSpace(args.IP)
	if net.ParseIP(ip) == nil {
		return Errorf("%q is not a valid IP address", ip)
	}
	u := "http://ip-api.com/json/" + ip + "?fields=status,message,country,regionName,city,zip,lat,lon,timezone,isp,org,as,reverse,query"
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := webClient.Do(req)
	if err != nil {
		return Errorf("ip lookup failed: %v", err)
	}
	defer resp.Body.Close()
	var d struct {
		Status, Message, Country, RegionName, City, Zip, Timezone, ISP, Org, As, Reverse, Query string
		Lat, Lon                                                                                float64
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return Errorf("decode failed: %v", err)
	}
	if d.Status != "success" {
		msg := d.Message
		if msg == "" {
			msg = "unknown error"
		}
		return Errorf("lookup failed: %s", msg)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "IP intelligence for %s\n\n", d.Query)
	fmt.Fprintf(&b, "Location: %s, %s, %s %s\n", d.City, d.RegionName, d.Country, d.Zip)
	fmt.Fprintf(&b, "Coordinates: %.4f, %.4f (%s)\n", d.Lat, d.Lon, d.Timezone)
	fmt.Fprintf(&b, "ISP: %s\nOrg: %s\nASN: %s\n", d.ISP, d.Org, d.As)
	if d.Reverse != "" {
		fmt.Fprintf(&b, "Reverse DNS: %s\n", d.Reverse)
	}
	return Text(b.String())
}

// ---- osint_username ---------------------------------------------------------

type osintUsernameTool struct{}

func (osintUsernameTool) Name() string { return "osint_username" }
func (osintUsernameTool) Description() string {
	return "Search for a username across many public platforms and report where a matching public " +
		"profile exists. For authorized investigations and footprint reviews of accounts you are permitted to assess."
}
func (osintUsernameTool) Schema() map[string]any {
	return schema(map[string]any{
		"username": prop("string", "The username / handle to search for."),
	}, "username")
}
func (osintUsernameTool) RequiresApproval() bool { return false }

// usernameSite is a platform probe: request Profile (with %s = username) and
// decide "found" by HTTP status, or by Absent text appearing in the body.
type usernameSite struct {
	Name    string
	Profile string
	Absent  string // if set, a 200 that contains this string means "not found"
}

var usernameSites = []usernameSite{
	{Name: "GitHub", Profile: "https://github.com/%s"},
	{Name: "GitLab", Profile: "https://gitlab.com/%s"},
	{Name: "Twitter/X", Profile: "https://twitter.com/%s"},
	{Name: "Instagram", Profile: "https://www.instagram.com/%s/"},
	{Name: "Reddit", Profile: "https://www.reddit.com/user/%s"},
	{Name: "TikTok", Profile: "https://www.tiktok.com/@%s"},
	{Name: "Telegram", Profile: "https://t.me/%s"},
	{Name: "Medium", Profile: "https://medium.com/@%s"},
	{Name: "Pinterest", Profile: "https://www.pinterest.com/%s/"},
	{Name: "Twitch", Profile: "https://www.twitch.tv/%s"},
	{Name: "YouTube", Profile: "https://www.youtube.com/@%s"},
	{Name: "Facebook", Profile: "https://www.facebook.com/%s"},
	{Name: "Steam", Profile: "https://steamcommunity.com/id/%s"},
	{Name: "Keybase", Profile: "https://keybase.io/%s"},
	{Name: "HackerNews", Profile: "https://news.ycombinator.com/user?id=%s", Absent: "No such user."},
	{Name: "Replit", Profile: "https://replit.com/@%s"},
	{Name: "Dev.to", Profile: "https://dev.to/%s"},
	{Name: "Kaggle", Profile: "https://www.kaggle.com/%s"},
	{Name: "PyPI", Profile: "https://pypi.org/user/%s/"},
	{Name: "npm", Profile: "https://www.npmjs.com/~%s"},
}

func (osintUsernameTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Username string `json:"username"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	user := strings.TrimSpace(args.Username)
	if user == "" {
		return Errorf("username is required")
	}

	// Base sites plus the broad platform database.
	sites := append(append([]usernameSite{}, usernameSites...), moreUsernameSites...)

	type hit struct {
		site  string
		url   string
		found bool
	}
	results := make([]hit, len(sites))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, s := range sites {
		wg.Add(1)
		go func(i int, s usernameSite) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			url := fmt.Sprintf(s.Profile, url.PathEscape(user))
			results[i] = hit{site: s.Name, url: url, found: probeUsername(ctx, url, s.Absent)}
		}(i, s)
	}
	wg.Wait()

	found := []hit{}
	for _, r := range results {
		if r.found {
			found = append(found, r)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].site < found[j].site })

	var b strings.Builder
	fmt.Fprintf(&b, "Username search for %q — %d/%d platforms matched:\n\n", user, len(found), len(sites))
	if len(found) == 0 {
		b.WriteString("No public profiles found on the checked platforms.\n")
	}
	for _, r := range found {
		fmt.Fprintf(&b, "- %s: %s\n", r.site, r.url)
	}
	return Result{Content: b.String(), Meta: map[string]any{"username": user, "matches": len(found)}}
}

func probeUsername(ctx context.Context, url, absent string) bool {
	tctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, "GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; antares-osint/1.0)")
	resp, err := webClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return false
	}
	if resp.StatusCode >= 400 {
		return false
	}
	if absent != "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		if strings.Contains(string(body), absent) {
			return false
		}
	}
	return true
}
