package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Remaining native OSINT tools, completing the reconnaissance surface: domain
// profile, phone triage, web scraping for contacts, paste-site search, IP
// enrichment (Censys / IP2Location), and a combined footprint.

// ---- osint_domain -----------------------------------------------------------

type osintDomainTool struct{}

func (osintDomainTool) Name() string { return "osint_domain" }
func (osintDomainTool) Description() string {
	return "Profile a domain in one call: resolved IPs, mail and name servers, and key WHOIS facts " +
		"(registrar, creation/expiry). Keyless."
}
func (osintDomainTool) Schema() map[string]any {
	return schema(map[string]any{"domain": prop("string", "The domain to profile.")}, "domain")
}
func (osintDomainTool) RequiresApproval() bool { return false }

func (osintDomainTool) Execute(ctx context.Context, in Input) Result {
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
	fmt.Fprintf(&b, "Domain profile: %s\n\n", domain)
	if ips, err := r.LookupHost(tctx, domain); err == nil {
		fmt.Fprintf(&b, "IPs: %s\n", strings.Join(ips, ", "))
	}
	if mx, err := r.LookupMX(tctx, domain); err == nil && len(mx) > 0 {
		hosts := make([]string, 0, len(mx))
		for _, m := range mx {
			hosts = append(hosts, strings.TrimSuffix(m.Host, "."))
		}
		fmt.Fprintf(&b, "Mail servers: %s\n", strings.Join(hosts, ", "))
	}
	if ns, err := r.LookupNS(tctx, domain); err == nil && len(ns) > 0 {
		hosts := make([]string, 0, len(ns))
		for _, n := range ns {
			hosts = append(hosts, strings.TrimSuffix(n.Host, "."))
		}
		fmt.Fprintf(&b, "Name servers: %s\n", strings.Join(hosts, ", "))
	}
	// WHOIS key fields.
	if root, err := whoisQuery(ctx, "whois.iana.org:43", domain); err == nil {
		server := ""
		for _, line := range strings.Split(root, "\n") {
			if v, ok := cutPrefixFold(line, "refer:"); ok {
				server = strings.TrimSpace(v)
				break
			}
		}
		body := root
		if server != "" {
			if wb, err := whoisQuery(ctx, server+":43", domain); err == nil && strings.TrimSpace(wb) != "" {
				body = wb
			}
		}
		b.WriteString("\nWHOIS:\n")
		for _, key := range []string{"Registrar:", "Creation Date:", "Registry Expiry Date:", "Updated Date:", "Registrant Organization:", "Domain Status:"} {
			for _, line := range strings.Split(body, "\n") {
				if v, ok := cutPrefixFold(strings.TrimSpace(line), key); ok {
					fmt.Fprintf(&b, "  %s %s\n", key, strings.TrimSpace(v))
					break
				}
			}
		}
	}
	return Text(b.String())
}

// ---- osint_phone ------------------------------------------------------------

type osintPhoneTool struct{}

func (osintPhoneTool) Name() string { return "osint_phone" }
func (osintPhoneTool) Description() string {
	return "Triage a phone number in E.164 form (+countrycode...): detect the country/region from its calling " +
		"code and normalize it. Keyless."
}
func (osintPhoneTool) Schema() map[string]any {
	return schema(map[string]any{"phone": prop("string", "The phone number, ideally with a leading + and country code.")}, "phone")
}
func (osintPhoneTool) RequiresApproval() bool { return false }

func (osintPhoneTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Phone string `json:"phone"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	raw := args.Phone
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, raw)
	if digits == "" {
		return Errorf("no digits in %q", raw)
	}
	// Match the longest known calling code (1–3 digits).
	country, code := "", ""
	for l := 3; l >= 1; l-- {
		if len(digits) >= l {
			if c, ok := callingCodes[digits[:l]]; ok {
				country, code = c, digits[:l]
				break
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Phone triage: %s\n\n", raw)
	fmt.Fprintf(&b, "Digits: %s\n", digits)
	if country != "" {
		fmt.Fprintf(&b, "Country code: +%s (%s)\n", code, country)
		fmt.Fprintf(&b, "National number: %s\n", digits[len(code):])
	} else {
		b.WriteString("Country code: not recognized — include a leading + and country code for accurate detection.\n")
	}
	fmt.Fprintf(&b, "E.164: +%s\n", digits)
	return Text(b.String())
}

var callingCodes = map[string]string{
	"1": "United States/Canada", "7": "Russia/Kazakhstan", "20": "Egypt", "27": "South Africa",
	"30": "Greece", "31": "Netherlands", "32": "Belgium", "33": "France", "34": "Spain",
	"36": "Hungary", "39": "Italy", "40": "Romania", "41": "Switzerland", "43": "Austria",
	"44": "United Kingdom", "45": "Denmark", "46": "Sweden", "47": "Norway", "48": "Poland",
	"49": "Germany", "51": "Peru", "52": "Mexico", "53": "Cuba", "54": "Argentina", "55": "Brazil",
	"56": "Chile", "57": "Colombia", "58": "Venezuela", "60": "Malaysia", "61": "Australia",
	"62": "Indonesia", "63": "Philippines", "64": "New Zealand", "65": "Singapore", "66": "Thailand",
	"81": "Japan", "82": "South Korea", "84": "Vietnam", "86": "China", "90": "Turkey",
	"91": "India", "92": "Pakistan", "93": "Afghanistan", "94": "Sri Lanka", "95": "Myanmar",
	"98": "Iran", "212": "Morocco", "213": "Algeria", "234": "Nigeria", "254": "Kenya",
	"351": "Portugal", "352": "Luxembourg", "353": "Ireland", "358": "Finland", "380": "Ukraine",
	"420": "Czechia", "421": "Slovakia", "852": "Hong Kong", "880": "Bangladesh", "886": "Taiwan",
	"960": "Maldives", "961": "Lebanon", "962": "Jordan", "966": "Saudi Arabia", "971": "UAE",
	"972": "Israel", "974": "Qatar", "977": "Nepal", "992": "Tajikistan", "994": "Azerbaijan",
	"998": "Uzbekistan",
}

// ---- osint_scrape -----------------------------------------------------------

var (
	reEmail  = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	reSocial = regexp.MustCompile(`https?://(?:www\.)?(?:twitter|x|facebook|instagram|linkedin|github|t\.me|youtube|tiktok)\.com/[A-Za-z0-9_./\-@]+`)
	reTitle  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

type osintScrapeTool struct{}

func (osintScrapeTool) Name() string { return "osint_scrape" }
func (osintScrapeTool) Description() string {
	return "Fetch a web page and extract contact intelligence from it: email addresses, social-media profile " +
		"links, and the page title. Keyless. For authorized reconnaissance."
}
func (osintScrapeTool) Schema() map[string]any {
	return schema(map[string]any{"url": prop("string", "The page URL to scrape.")}, "url")
}
func (osintScrapeTool) RequiresApproval() bool { return false }

func (osintScrapeTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		URL string `json:"url"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	u := strings.TrimSpace(args.URL)
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(tctx, "GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; antares-osint/1.0)")
	resp, err := webClient.Do(req)
	if err != nil {
		return Errorf("fetch failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	html := string(body)

	var b strings.Builder
	fmt.Fprintf(&b, "Scrape of %s (HTTP %d)\n\n", u, resp.StatusCode)
	if m := reTitle.FindStringSubmatch(html); len(m) > 1 {
		fmt.Fprintf(&b, "Title: %s\n", strings.TrimSpace(m[1]))
	}
	emails := uniqueStrings(reEmail.FindAllString(html, -1))
	if len(emails) > 0 {
		fmt.Fprintf(&b, "\nEmails (%d):\n%s\n", len(emails), strings.Join(capStrings(emails, 40), "\n"))
	}
	socials := uniqueStrings(reSocial.FindAllString(html, -1))
	if len(socials) > 0 {
		fmt.Fprintf(&b, "\nSocial links (%d):\n%s\n", len(socials), strings.Join(capStrings(socials, 40), "\n"))
	}
	if len(emails) == 0 && len(socials) == 0 {
		b.WriteString("\nNo emails or social links found.\n")
	}
	return Text(b.String())
}

// ---- osint_paste ------------------------------------------------------------

type osintPasteTool struct{}

func (osintPasteTool) Name() string { return "osint_paste" }
func (osintPasteTool) Description() string {
	return "Search public paste dumps for a term (email, username, domain, keyword) via psbdmp. Keyless. For " +
		"authorized exposure checks."
}
func (osintPasteTool) Schema() map[string]any {
	return schema(map[string]any{"query": prop("string", "The term to search for in paste dumps.")}, "query")
}
func (osintPasteTool) RequiresApproval() bool { return false }

func (osintPasteTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Query string `json:"query"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return Errorf("query is required")
	}
	var d struct {
		Count int `json:"count"`
		Data  []struct {
			ID   string `json:"id"`
			Tags string `json:"tags"`
			Time string `json:"time"`
		} `json:"data"`
	}
	status, err := osintJSON(ctx, "GET", "https://psbdmp.ws/api/v3/search/"+q, nil, &d)
	if err != nil {
		return Errorf("paste search failed: %v", err)
	}
	if status != 200 {
		return Errorf("paste search returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Paste search for %q — %d dump(s):\n\n", q, len(d.Data))
	if len(d.Data) == 0 {
		b.WriteString("No paste dumps found.\n")
	}
	for i, p := range d.Data {
		if i >= 30 {
			break
		}
		fmt.Fprintf(&b, "- https://psbdmp.ws/%s (%s) %s\n", p.ID, p.Time, p.Tags)
	}
	return Text(b.String())
}

// ---- osint_censys -----------------------------------------------------------

type osintCensysTool struct{}

func (osintCensysTool) Name() string { return "osint_censys" }
func (osintCensysTool) Description() string {
	return "Look up an IP on Censys: open services, protocols, and location. Requires Censys API credentials " +
		"stored as osint:censys in the form \"id:secret\"."
}
func (osintCensysTool) Schema() map[string]any {
	return schema(map[string]any{"ip": prop("string", "The IP address to look up.")}, "ip")
}
func (osintCensysTool) RequiresApproval() bool { return false }

func (osintCensysTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		IP string `json:"ip"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	ip := strings.TrimSpace(args.IP)
	if ip == "" {
		return Errorf("ip is required")
	}
	cred := osintKey(ctx, in, "censys", "CENSYS_CREDS")
	if cred == "" || !strings.Contains(cred, ":") {
		return Errorf("no Censys credentials — set them as osint:censys in the form \"id:secret\", then retry.")
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
	var d struct {
		Result struct {
			Services []struct {
				Port              int    `json:"port"`
				ServiceName       string `json:"service_name"`
				TransportProtocol string `json:"transport_protocol"`
			} `json:"services"`
			Location struct {
				Country string `json:"country"`
				City    string `json:"city"`
			} `json:"location"`
			AutonomousSystem struct {
				Name string `json:"name"`
			} `json:"autonomous_system"`
		} `json:"result"`
	}
	status, err := osintJSON(ctx, "GET", "https://search.censys.io/api/v2/hosts/"+ip,
		map[string]string{"Authorization": auth}, &d)
	if err != nil {
		return Errorf("Censys lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("Censys has no record for %s.", ip))
	}
	if status != 200 {
		return Errorf("Censys returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Censys for %s\n\n", ip)
	fmt.Fprintf(&b, "Location: %s, %s\nAS: %s\n\nServices:\n", d.Result.Location.City, d.Result.Location.Country, d.Result.AutonomousSystem.Name)
	for _, s := range d.Result.Services {
		fmt.Fprintf(&b, "- %d/%s %s\n", s.Port, s.TransportProtocol, s.ServiceName)
	}
	return Text(b.String())
}

// ---- osint_ip2location ------------------------------------------------------

type osintIP2LocationTool struct{}

func (osintIP2LocationTool) Name() string { return "osint_ip2location" }
func (osintIP2LocationTool) Description() string {
	return "Enrich an IP via IP2Location.io: geolocation, ISP, domain, usage type, and proxy/VPN flags. " +
		"Requires an IP2Location key (osint:ip2location)."
}
func (osintIP2LocationTool) Schema() map[string]any {
	return schema(map[string]any{"ip": prop("string", "The IP address to enrich.")}, "ip")
}
func (osintIP2LocationTool) RequiresApproval() bool { return false }

func (osintIP2LocationTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		IP string `json:"ip"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	ip := strings.TrimSpace(args.IP)
	if ip == "" {
		return Errorf("ip is required")
	}
	key := osintKey(ctx, in, "ip2location", "IP2LOCATION_API_KEY")
	if key == "" {
		return Errorf("no IP2Location key — set one as osint:ip2location, then retry.")
	}
	var d struct {
		CountryName, RegionName, CityName, ISP, Domain, UsageType, ASN, As string
		IsProxy                                                            bool `json:"is_proxy"`
	}
	status, err := osintJSON(ctx, "GET", "https://api.ip2location.io/?key="+key+"&ip="+ip, nil, &d)
	if err != nil {
		return Errorf("IP2Location lookup failed: %v", err)
	}
	if status != 200 {
		return Errorf("IP2Location returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "IP2Location for %s\n\n", ip)
	fmt.Fprintf(&b, "Location: %s, %s, %s\n", d.CityName, d.RegionName, d.CountryName)
	fmt.Fprintf(&b, "ISP: %s | Domain: %s | Usage: %s\n", d.ISP, d.Domain, d.UsageType)
	writeIf(&b, "AS", firstNonBlank(d.As, d.ASN))
	if d.IsProxy {
		b.WriteString("Proxy/VPN: yes\n")
	}
	return Text(b.String())
}

// ---- osint_footprint --------------------------------------------------------

type osintFootprintTool struct{}

func (osintFootprintTool) Name() string { return "osint_footprint" }
func (osintFootprintTool) Description() string {
	return "Build a combined footprint for a target: for a domain, resolve DNS + WHOIS + dorks; for a username, " +
		"search platforms + GitHub + dorks. One call to orient an investigation. Keyless core."
}
func (osintFootprintTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string", "A domain or a username/handle."),
	}, "target")
}
func (osintFootprintTool) RequiresApproval() bool { return false }

func (osintFootprintTool) Execute(ctx context.Context, in Input) Result {
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
	fmt.Fprintf(&b, "Footprint for %q\n\n", target)
	isDomain := strings.Contains(target, ".") && !strings.Contains(target, "@") && !strings.Contains(target, " ")
	if isDomain {
		b.WriteString("== DNS ==\n")
		b.WriteString(osintDNSTool{}.Execute(ctx, wrapArg("domain", target)).Content)
		b.WriteString("\n== WHOIS ==\n")
		b.WriteString(osintWhoisTool{}.Execute(ctx, wrapArg("domain", target)).Content)
	} else {
		b.WriteString("== Usernames ==\n")
		b.WriteString(osintUsernameTool{}.Execute(ctx, wrapArg("username", target)).Content)
		b.WriteString("\n== GitHub ==\n")
		b.WriteString(osintGithubTool{}.Execute(ctx, wrapArgWithDeps("username", target, in.Deps)).Content)
	}
	b.WriteString("\n== Dorks ==\n")
	b.WriteString(osintDorksTool{}.Execute(ctx, wrapArg("target", target)).Content)
	return Text(b.String())
}

// ---- helpers ----------------------------------------------------------------

func wrapArg(key, val string) Input {
	return Input{Args: []byte(fmt.Sprintf(`{%q:%q}`, key, val)), Emit: func(Progress) {}}
}
func wrapArgWithDeps(key, val string, deps *Deps) Input {
	in := wrapArg(key, val)
	in.Deps = deps
	return in
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func capStrings(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}
