package tools

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Service-backed OSINT lookups — all keyless. They use free, public endpoints
// (no registration or API key), so every OSINT tool works out of the box.

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

// ---- osint_github (keyless) -------------------------------------------------

type osintGithubTool struct{}

func (osintGithubTool) Name() string { return "osint_github" }
func (osintGithubTool) Description() string {
	return "Profile a GitHub user from public data: name, bio, company, location, blog, public repo/follower " +
		"counts, and account age. Keyless."
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
	var u struct {
		Login, Name, Company, Blog, Location, Email, Bio, CreatedAt string
		PublicRepos, Followers, Following                           int
	}
	status, err := osintJSON(ctx, "GET", "https://api.github.com/users/"+user, nil, &u)
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

// ---- osint_breach (keyless, XposedOrNot) ------------------------------------

// xposedBreaches queries the keyless XposedOrNot API for breaches of an email.
func xposedBreaches(ctx context.Context, email string) (names []string, found bool, err error) {
	var d struct {
		Breaches [][]string `json:"breaches"`
		Error    string     `json:"Error"`
	}
	status, e := osintJSON(ctx, "GET", "https://api.xposedornot.com/v1/check-email/"+email, nil, &d)
	if e != nil {
		return nil, false, e
	}
	if status == 404 || d.Error != "" {
		return nil, false, nil
	}
	if len(d.Breaches) > 0 {
		return d.Breaches[0], len(d.Breaches[0]) > 0, nil
	}
	return nil, false, nil
}

type osintBreachTool struct{}

func (osintBreachTool) Name() string { return "osint_breach" }
func (osintBreachTool) Description() string {
	return "Check whether an email appears in known public data breaches. Keyless (via XposedOrNot). For " +
		"authorized investigations."
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
	names, found, err := xposedBreaches(ctx, email)
	if err != nil {
		return Errorf("breach lookup failed: %v", err)
	}
	if !found {
		return Text(fmt.Sprintf("%s: no breaches found.", email))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s appears in %d breach(es):\n\n", email, len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return Text(b.String())
}

// ---- osint_email (keyless) --------------------------------------------------

type osintEmailTool struct{}

func (osintEmailTool) Name() string { return "osint_email" }
func (osintEmailTool) Description() string {
	return "Investigate an email address: Gravatar profile presence and known data-breach exposure. Keyless. " +
		"For authorized investigations."
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

	// Breaches (keyless).
	names, found, err := xposedBreaches(ctx, email)
	switch {
	case err != nil:
		b.WriteString("Breaches: lookup failed\n")
	case !found:
		b.WriteString("Breaches: none found\n")
	default:
		fmt.Fprintf(&b, "Breaches: %d found:\n", len(names))
		for _, n := range names {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	return Text(b.String())
}

// ---- osint_shodan (keyless, Shodan InternetDB) ------------------------------

type osintShodanTool struct{}

func (osintShodanTool) Name() string { return "osint_shodan" }
func (osintShodanTool) Description() string {
	return "Look up an IP's exposed surface: open ports, hostnames, technologies (CPEs), and known CVEs. " +
		"Keyless (via Shodan's free InternetDB)."
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
	var d struct {
		IP        string   `json:"ip"`
		Ports     []int    `json:"ports"`
		Hostnames []string `json:"hostnames"`
		Cpes      []string `json:"cpes"`
		Vulns     []string `json:"vulns"`
		Tags      []string `json:"tags"`
	}
	status, err := osintJSON(ctx, "GET", "https://internetdb.shodan.io/"+ip, nil, &d)
	if err != nil {
		return Errorf("lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("No exposed-surface records for %s.", ip))
	}
	if status != 200 {
		return Errorf("InternetDB returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Exposed surface for %s (Shodan InternetDB)\n\n", ip)
	if len(d.Hostnames) > 0 {
		fmt.Fprintf(&b, "Hostnames: %s\n", strings.Join(d.Hostnames, ", "))
	}
	fmt.Fprintf(&b, "Open ports: %s\n", intsJoin(d.Ports))
	if len(d.Cpes) > 0 {
		fmt.Fprintf(&b, "Technologies: %s\n", strings.Join(d.Cpes, ", "))
	}
	if len(d.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(d.Tags, ", "))
	}
	if len(d.Vulns) > 0 {
		fmt.Fprintf(&b, "\n⚠ Known CVEs: %s\n", strings.Join(d.Vulns, ", "))
	} else {
		b.WriteString("\nKnown CVEs: none listed\n")
	}
	return Text(b.String())
}

// ---- osint_reputation (keyless, urlscan.io) ---------------------------------

type osintReputationTool struct{}

func (osintReputationTool) Name() string { return "osint_reputation" }
func (osintReputationTool) Description() string {
	return "Check a domain or URL's public scan history and threat sightings via urlscan.io: how many scans " +
		"exist, the pages seen, and any results flagged malicious. Keyless."
}
func (osintReputationTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string", "A domain (e.g. example.com) or URL to check."),
	}, "target")
}
func (osintReputationTool) RequiresApproval() bool { return false }

func (osintReputationTool) Execute(ctx context.Context, in Input) Result {
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
	q := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://")
	q = strings.SplitN(q, "/", 2)[0]

	var d struct {
		Total   int `json:"total"`
		Results []struct {
			Task struct {
				URL  string `json:"url"`
				Time string `json:"time"`
			} `json:"task"`
			Verdicts struct {
				Overall struct {
					Malicious bool `json:"malicious"`
					Score     int  `json:"score"`
				} `json:"overall"`
			} `json:"verdicts"`
		} `json:"results"`
	}
	status, err := osintJSON(ctx, "GET", "https://urlscan.io/api/v1/search/?q=domain:"+q+"&size=10", nil, &d)
	if err != nil {
		return Errorf("reputation lookup failed: %v", err)
	}
	if status != 200 {
		return Errorf("urlscan returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Reputation for %s (urlscan.io)\n\n", q)
	fmt.Fprintf(&b, "Public scans on record: %d\n", d.Total)
	malicious := 0
	for _, r := range d.Results {
		if r.Verdicts.Overall.Malicious {
			malicious++
		}
	}
	if malicious > 0 {
		fmt.Fprintf(&b, "⚠ %d recent scan(s) flagged MALICIOUS\n", malicious)
	}
	if len(d.Results) > 0 {
		b.WriteString("\nRecent scans:\n")
		for i, r := range d.Results {
			if i >= 8 {
				break
			}
			flag := ""
			if r.Verdicts.Overall.Malicious {
				flag = " [malicious]"
			}
			fmt.Fprintf(&b, "- %s (%s)%s\n", r.Task.URL, r.Task.Time, flag)
		}
	} else {
		b.WriteString("\nNo public scans found for this target.\n")
	}
	return Text(b.String())
}

// ---- osint_crypto (keyless) -------------------------------------------------

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
		NTx           int   `json:"n_tx"`
		TotalReceived int64 `json:"total_received"`
		TotalSent     int64 `json:"total_sent"`
		FinalBalance  int64 `json:"final_balance"`
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

func intsJoin(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}
