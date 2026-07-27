package tools

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// This file extends the native OSINT toolset with service-backed lookups. Keys
// for the paid/registered services are read from the store's KV under
// "osint:<service>" (or an env-var fallback); a missing key returns a clear,
// actionable message rather than failing the turn.

// osintKey resolves an API key for a service: KV "osint:<service>" first, then
// the given environment variables. Returns "" when unset.
func osintKey(ctx context.Context, in Input, service string, envs ...string) string {
	if in.Deps != nil && in.Deps.Store != nil {
		if v, err := in.Deps.Store.GetKV(ctx, "osint:"+service); err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, e := range envs {
		if v := strings.TrimSpace(os.Getenv(e)); v != "" {
			return v
		}
	}
	return ""
}

func osintJSON(ctx context.Context, method, url string, headers map[string]string, out any) (int, error) {
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, method, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "antares-osint/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := webClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if out != nil && len(body) > 0 {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode, nil
}

// ---- osint_github -----------------------------------------------------------

type osintGithubTool struct{}

func (osintGithubTool) Name() string { return "osint_github" }
func (osintGithubTool) Description() string {
	return "Profile a GitHub user from public data: name, bio, company, location, blog, public repo/follower " +
		"counts, and account age. Optional token (osint:github) raises rate limits."
}
func (osintGithubTool) Schema() map[string]any {
	return schema(map[string]any{"username": prop("string", "The GitHub login to profile.")}, "username")
}
func (osintGithubTool) RequiresApproval() bool { return false }

func (osintGithubTool) Execute(ctx context.Context, in Input) Result {
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
	headers := map[string]string{}
	if tok := osintKey(ctx, in, "github", "GITHUB_TOKEN"); tok != "" {
		headers["Authorization"] = "Bearer " + tok
	}
	var u struct {
		Login, Name, Company, Blog, Location, Email, Bio, CreatedAt string
		PublicRepos, Followers, Following                           int
	}
	status, err := osintJSON(ctx, "GET", "https://api.github.com/users/"+user, headers, &u)
	if err != nil {
		return Errorf("github lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("No GitHub user %q.", user))
	}
	if status != 200 {
		return Errorf("github returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GitHub: %s\n\n", u.Login)
	writeIf(&b, "Name", u.Name)
	writeIf(&b, "Bio", u.Bio)
	writeIf(&b, "Company", u.Company)
	writeIf(&b, "Location", u.Location)
	writeIf(&b, "Blog", u.Blog)
	writeIf(&b, "Email", u.Email)
	fmt.Fprintf(&b, "Repos: %d | Followers: %d | Following: %d\n", u.PublicRepos, u.Followers, u.Following)
	writeIf(&b, "Joined", u.CreatedAt)
	fmt.Fprintf(&b, "Profile: https://github.com/%s\n", u.Login)
	return Text(b.String())
}

func writeIf(b *strings.Builder, label, val string) {
	if strings.TrimSpace(val) != "" {
		fmt.Fprintf(b, "%s: %s\n", label, val)
	}
}

// ---- osint_email ------------------------------------------------------------

type osintEmailTool struct{}

func (osintEmailTool) Name() string { return "osint_email" }
func (osintEmailTool) Description() string {
	return "Investigate an email address: Gravatar profile presence (keyless) and, if an HIBP key is set " +
		"(osint:hibp), known data-breach exposure. For authorized investigations."
}
func (osintEmailTool) Schema() map[string]any {
	return schema(map[string]any{"email": prop("string", "The email address to investigate.")}, "email")
}
func (osintEmailTool) RequiresApproval() bool { return false }

func (osintEmailTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Email string `json:"email"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	email := strings.TrimSpace(strings.ToLower(args.Email))
	if !strings.Contains(email, "@") {
		return Errorf("%q is not a valid email", email)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Email intelligence for %s\n\n", email)

	// Gravatar (keyless): an existing hash returns a profile.
	sum := md5.Sum([]byte(email))
	hash := hex.EncodeToString(sum[:])
	var grav struct {
		Entry []struct {
			DisplayName string `json:"displayName"`
			AboutMe     string `json:"aboutMe"`
			ProfileUrl  string `json:"profileUrl"`
		} `json:"entry"`
	}
	if status, _ := osintJSON(ctx, "GET", "https://www.gravatar.com/"+hash+".json", nil, &grav); status == 200 && len(grav.Entry) > 0 {
		e := grav.Entry[0]
		b.WriteString("Gravatar: found\n")
		writeIf(&b, "  Name", e.DisplayName)
		writeIf(&b, "  About", e.AboutMe)
		writeIf(&b, "  Profile", e.ProfileUrl)
	} else {
		b.WriteString("Gravatar: none\n")
	}

	// HIBP breaches (key required).
	if key := osintKey(ctx, in, "hibp", "HIBP_API_KEY"); key != "" {
		var breaches []struct{ Name, BreachDate string }
		status, _ := osintJSON(ctx, "GET",
			"https://haveibeenpwned.com/api/v3/breachedaccount/"+email+"?truncateResponse=false",
			map[string]string{"hibp-api-key": key}, &breaches)
		switch {
		case status == 404:
			b.WriteString("Breaches: none found (HIBP)\n")
		case status == 200:
			fmt.Fprintf(&b, "Breaches: %d found (HIBP):\n", len(breaches))
			for _, br := range breaches {
				fmt.Fprintf(&b, "  - %s (%s)\n", br.Name, br.BreachDate)
			}
		default:
			fmt.Fprintf(&b, "Breaches: HIBP returned HTTP %d\n", status)
		}
	} else {
		b.WriteString("Breaches: skipped — add an HIBP key on the API Keys page to enable.\n")
	}
	return Text(b.String())
}

// ---- osint_breach -----------------------------------------------------------

type osintBreachTool struct{}

func (osintBreachTool) Name() string { return "osint_breach" }
func (osintBreachTool) Description() string {
	return "Check whether an email appears in known public data breaches via HaveIBeenPwned. Requires an " +
		"HIBP API key (osint:hibp). For authorized investigations."
}
func (osintBreachTool) Schema() map[string]any {
	return schema(map[string]any{"email": prop("string", "The email address to check.")}, "email")
}
func (osintBreachTool) RequiresApproval() bool { return false }

func (osintBreachTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Email string `json:"email"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	email := strings.TrimSpace(strings.ToLower(args.Email))
	if !strings.Contains(email, "@") {
		return Errorf("%q is not a valid email", email)
	}
	key := osintKey(ctx, in, "hibp", "HIBP_API_KEY")
	if key == "" {
		return Errorf("no HIBP API key — add it on the API Keys page in Settings, then retry.")
	}
	var breaches []struct {
		Name, Title, BreachDate, Domain string
		PwnCount                        int
		DataClasses                     []string
	}
	status, err := osintJSON(ctx, "GET",
		"https://haveibeenpwned.com/api/v3/breachedaccount/"+email+"?truncateResponse=false",
		map[string]string{"hibp-api-key": key}, &breaches)
	if err != nil {
		return Errorf("HIBP lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("%s: no breaches found.", email))
	}
	if status != 200 {
		return Errorf("HIBP returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s appears in %d breach(es):\n\n", email, len(breaches))
	for _, br := range breaches {
		fmt.Fprintf(&b, "- %s (%s, %s) — %d accounts\n  data: %s\n",
			firstNonBlank(br.Title, br.Name), br.Domain, br.BreachDate, br.PwnCount, strings.Join(br.DataClasses, ", "))
	}
	return Text(b.String())
}

// ---- osint_virustotal -------------------------------------------------------

type osintVirusTotalTool struct{}

func (osintVirusTotalTool) Name() string { return "osint_virustotal" }
func (osintVirusTotalTool) Description() string {
	return "Query VirusTotal for a domain, IP, or file hash: how many engines flag it malicious/suspicious " +
		"plus reputation. Requires a VirusTotal API key (osint:virustotal)."
}
func (osintVirusTotalTool) Schema() map[string]any {
	return schema(map[string]any{
		"indicator": prop("string", "A domain, IP address, or file hash (MD5/SHA-1/SHA-256)."),
	}, "indicator")
}
func (osintVirusTotalTool) RequiresApproval() bool { return false }

func (osintVirusTotalTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Indicator string `json:"indicator"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	ind := strings.TrimSpace(args.Indicator)
	if ind == "" {
		return Errorf("indicator is required")
	}
	key := osintKey(ctx, in, "virustotal", "VT_API_KEY", "VIRUSTOTAL_API_KEY")
	if key == "" {
		return Errorf("no VirusTotal API key — add it on the API Keys page in Settings, then retry.")
	}
	kind, id := vtEndpoint(ind)
	var d struct {
		Data struct {
			Attributes struct {
				LastAnalysisStats struct {
					Harmless, Malicious, Suspicious, Undetected, Timeout int
				} `json:"last_analysis_stats"`
				Reputation int `json:"reputation"`
			} `json:"attributes"`
		} `json:"data"`
	}
	status, err := osintJSON(ctx, "GET", "https://www.virustotal.com/api/v3/"+kind+"/"+id,
		map[string]string{"x-apikey": key}, &d)
	if err != nil {
		return Errorf("VirusTotal lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("VirusTotal has no record for %s.", ind))
	}
	if status != 200 {
		return Errorf("VirusTotal returned HTTP %d", status)
	}
	s := d.Data.Attributes.LastAnalysisStats
	var b strings.Builder
	fmt.Fprintf(&b, "VirusTotal for %s (%s)\n\n", ind, kind)
	fmt.Fprintf(&b, "Malicious: %d | Suspicious: %d | Harmless: %d | Undetected: %d\n",
		s.Malicious, s.Suspicious, s.Harmless, s.Undetected)
	fmt.Fprintf(&b, "Reputation: %d\n", d.Data.Attributes.Reputation)
	if s.Malicious+s.Suspicious > 0 {
		b.WriteString("\n⚠ Flagged by one or more engines.\n")
	}
	return Text(b.String())
}

func vtEndpoint(ind string) (kind, id string) {
	switch {
	case len(ind) == 32 || len(ind) == 40 || len(ind) == 64:
		if isHex(ind) {
			return "files", ind
		}
	}
	if isIPish(ind) {
		return "ip_addresses", ind
	}
	return "domains", ind
}

// ---- osint_abuseipdb --------------------------------------------------------

type osintAbuseIPDBTool struct{}

func (osintAbuseIPDBTool) Name() string { return "osint_abuseipdb" }
func (osintAbuseIPDBTool) Description() string {
	return "Check an IP's abuse reputation on AbuseIPDB: confidence score, report count, country, ISP, and " +
		"usage type. Requires an AbuseIPDB API key (osint:abuseipdb)."
}
func (osintAbuseIPDBTool) Schema() map[string]any {
	return schema(map[string]any{"ip": prop("string", "The IP address to check.")}, "ip")
}
func (osintAbuseIPDBTool) RequiresApproval() bool { return false }

func (osintAbuseIPDBTool) Execute(ctx context.Context, in Input) Result {
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
	key := osintKey(ctx, in, "abuseipdb", "ABUSEIPDB_API_KEY")
	if key == "" {
		return Errorf("no AbuseIPDB API key — add it on the API Keys page in Settings, then retry.")
	}
	var d struct {
		Data struct {
			AbuseConfidenceScore int    `json:"abuseConfidenceScore"`
			TotalReports         int    `json:"totalReports"`
			CountryCode          string `json:"countryCode"`
			ISP                  string `json:"isp"`
			UsageType            string `json:"usageType"`
			Domain               string `json:"domain"`
			IsTor                bool   `json:"isTor"`
		} `json:"data"`
	}
	status, err := osintJSON(ctx, "GET",
		"https://api.abuseipdb.com/api/v2/check?ipAddress="+ip+"&maxAgeInDays=90",
		map[string]string{"Key": key}, &d)
	if err != nil {
		return Errorf("AbuseIPDB lookup failed: %v", err)
	}
	if status != 200 {
		return Errorf("AbuseIPDB returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "AbuseIPDB for %s\n\n", ip)
	fmt.Fprintf(&b, "Abuse confidence: %d%% (%d reports, 90d)\n", d.Data.AbuseConfidenceScore, d.Data.TotalReports)
	fmt.Fprintf(&b, "Country: %s | ISP: %s | Usage: %s\n", d.Data.CountryCode, d.Data.ISP, d.Data.UsageType)
	writeIf(&b, "Domain", d.Data.Domain)
	if d.Data.IsTor {
		b.WriteString("Tor exit node: yes\n")
	}
	return Text(b.String())
}

// ---- osint_shodan -----------------------------------------------------------

type osintShodanTool struct{}

func (osintShodanTool) Name() string { return "osint_shodan" }
func (osintShodanTool) Description() string {
	return "Look up an IP on Shodan: open ports, service banners, hostnames, org, OS, and known CVEs. " +
		"Requires a Shodan API key (osint:shodan)."
}
func (osintShodanTool) Schema() map[string]any {
	return schema(map[string]any{"ip": prop("string", "The IP address to look up.")}, "ip")
}
func (osintShodanTool) RequiresApproval() bool { return false }

func (osintShodanTool) Execute(ctx context.Context, in Input) Result {
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
	key := osintKey(ctx, in, "shodan", "SHODAN_API_KEY")
	if key == "" {
		return Errorf("no Shodan API key — add it on the API Keys page in Settings, then retry.")
	}
	var d struct {
		Ports     []int    `json:"ports"`
		Hostnames []string `json:"hostnames"`
		Org       string   `json:"org"`
		OS        string   `json:"os"`
		Vulns     []string `json:"vulns"`
	}
	status, err := osintJSON(ctx, "GET", "https://api.shodan.io/shodan/host/"+ip+"?key="+key, nil, &d)
	if err != nil {
		return Errorf("Shodan lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("Shodan has no record for %s.", ip))
	}
	if status != 200 {
		return Errorf("Shodan returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Shodan for %s\n\n", ip)
	writeIf(&b, "Org", d.Org)
	writeIf(&b, "OS", d.OS)
	if len(d.Hostnames) > 0 {
		fmt.Fprintf(&b, "Hostnames: %s\n", strings.Join(d.Hostnames, ", "))
	}
	fmt.Fprintf(&b, "Open ports: %s\n", intsJoin(d.Ports))
	if len(d.Vulns) > 0 {
		fmt.Fprintf(&b, "Known CVEs: %s\n", strings.Join(d.Vulns, ", "))
	}
	return Text(b.String())
}

// ---- osint_crypto -----------------------------------------------------------

type osintCryptoTool struct{}

func (osintCryptoTool) Name() string { return "osint_crypto" }
func (osintCryptoTool) Description() string {
	return "Look up a Bitcoin address's on-chain activity: balance, total received/sent, and transaction " +
		"count. Keyless (public blockchain data)."
}
func (osintCryptoTool) Schema() map[string]any {
	return schema(map[string]any{"address": prop("string", "The Bitcoin address to inspect.")}, "address")
}
func (osintCryptoTool) RequiresApproval() bool { return false }

func (osintCryptoTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Address string `json:"address"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	addr := strings.TrimSpace(args.Address)
	if addr == "" {
		return Errorf("address is required")
	}
	var d struct {
		Address       string `json:"address"`
		NTx           int    `json:"n_tx"`
		TotalReceived int64  `json:"total_received"`
		TotalSent     int64  `json:"total_sent"`
		FinalBalance  int64  `json:"final_balance"`
	}
	status, err := osintJSON(ctx, "GET", "https://blockchain.info/rawaddr/"+addr+"?limit=1", nil, &d)
	if err != nil {
		return Errorf("crypto lookup failed: %v", err)
	}
	if status != 200 {
		return Errorf("blockchain lookup returned HTTP %d (is the address valid?)", status)
	}
	btc := func(sat int64) float64 { return float64(sat) / 1e8 }
	var b strings.Builder
	fmt.Fprintf(&b, "Bitcoin address %s\n\n", addr)
	fmt.Fprintf(&b, "Balance: %.8f BTC\n", btc(d.FinalBalance))
	fmt.Fprintf(&b, "Total received: %.8f BTC\n", btc(d.TotalReceived))
	fmt.Fprintf(&b, "Total sent: %.8f BTC\n", btc(d.TotalSent))
	fmt.Fprintf(&b, "Transactions: %d\n", d.NTx)
	return Text(b.String())
}

// ---- helpers ----------------------------------------------------------------

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func isIPish(s string) bool {
	return strings.Count(s, ".") == 3 || strings.Contains(s, ":")
}

func intsJoin(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}
