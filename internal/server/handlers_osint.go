package server

import (
	"net/http"
	"strings"
)

// osintKeySpec describes a key-based OSINT service for the settings UI: what it
// is, why it helps, and where to get the key.
type osintKeySpec struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Howto       string `json:"howto"`
	HowtoURL    string `json:"howto_url"`
	Optional    bool   `json:"optional"`
}

var osintKeySpecs = []osintKeySpec{
	{
		ID: "virustotal", Label: "VirusTotal",
		Description: "Reputation for domains, IPs, and file hashes — how many engines flag them.",
		Howto:       "Create a free account, then copy your API key from your profile.",
		HowtoURL:    "https://www.virustotal.com/gui/my-apikey",
	},
	{
		ID: "shodan", Label: "Shodan",
		Description: "Open ports, service banners, and known CVEs for an IP.",
		Howto:       "Sign up, then copy the API key shown on your account page.",
		HowtoURL:    "https://account.shodan.io/",
	},
	{
		ID: "abuseipdb", Label: "AbuseIPDB",
		Description: "Abuse-confidence score and report history for an IP.",
		Howto:       "Register, open the API tab, and create a key.",
		HowtoURL:    "https://www.abuseipdb.com/account/api",
	},
	{
		ID: "hibp", Label: "Have I Been Pwned",
		Description: "Whether an email appears in known data breaches.",
		Howto:       "Purchase an API key (HIBP requires a paid key), then paste it here.",
		HowtoURL:    "https://haveibeenpwned.com/API/Key",
	},
	{
		ID: "censys", Label: "Censys",
		Description: "Hosts, services, and protocols for an IP. Enter as \"id:secret\".",
		Howto:       "Create an account, open API settings, and combine your API ID and secret as id:secret.",
		HowtoURL:    "https://search.censys.io/account/api",
	},
	{
		ID: "ip2location", Label: "IP2Location.io",
		Description: "Geolocation, ISP, usage type, and proxy/VPN flags for an IP.",
		Howto:       "Sign up (free tier available) and copy the API key from your dashboard.",
		HowtoURL:    "https://www.ip2location.io/",
	},
	{
		ID: "github", Label: "GitHub token", Optional: true,
		Description: "Optional — raises rate limits for GitHub profile lookups.",
		Howto:       "Create a fine-grained or classic personal access token (no scopes needed for public data).",
		HowtoURL:    "https://github.com/settings/tokens",
	},
}

func (s *Server) handleOsintKeys(w http.ResponseWriter, r *http.Request) {
	type item struct {
		osintKeySpec
		Set bool `json:"set"`
	}
	out := make([]item, 0, len(osintKeySpecs))
	for _, spec := range osintKeySpecs {
		v, _ := s.db.GetKV(r.Context(), "osint:"+spec.ID)
		out = append(out, item{osintKeySpec: spec, Set: strings.TrimSpace(v) != ""})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (s *Server) handleSetOsintKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// Only accept known service ids.
	known := false
	for _, spec := range osintKeySpecs {
		if spec.ID == body.ID {
			known = true
			break
		}
	}
	if !known {
		writeError(w, http.StatusBadRequest, errNotFound)
		return
	}
	key := "osint:" + body.ID
	if strings.TrimSpace(body.Value) == "" {
		_ = s.db.DeleteKV(r.Context(), key)
	} else {
		if err := s.db.SetKV(r.Context(), key, strings.TrimSpace(body.Value)); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
