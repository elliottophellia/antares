package httpshim

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// runWget emulates the common subset of wget over the fingerprinted client.
// wget's default is to save the body to a file named after the URL; -O- writes
// to stdout, which is the form the agent usually reaches for (wget -qO- URL).
// Anything not modelled falls back to the real wget.
func runWget(args []string) int {
	var (
		method  = "GET"
		url     string
		headers = map[string]string{}
		body    []byte
		output  string // -O; "-" means stdout; "" means default filename
		haveOut bool
		timeout time.Duration
	)

	i := 0
	nextVal := func(inline string, hasInline bool) (string, bool) {
		if hasInline {
			return inline, true
		}
		if i+1 >= len(args) {
			return "", false
		}
		i++
		return args[i], true
	}

	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			name, inline, hasInline := cutFlag(a)
			switch name {
			case "--output-document":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("wget", args)
				}
				output, haveOut = v, true
			case "--header":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("wget", args)
				}
				if k, val, ok := strings.Cut(v, ":"); ok {
					headers[strings.TrimSpace(k)] = strings.TrimSpace(val)
				}
			case "--post-data":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("wget", args)
				}
				body = []byte(v)
				method = "POST"
				if _, ok := lookupHeader(headers, "content-type"); !ok {
					headers["Content-Type"] = "application/x-www-form-urlencoded"
				}
			case "--user-agent":
				if v, ok := nextVal(inline, hasInline); ok {
					headers["User-Agent"] = v
				}
			case "--timeout", "--read-timeout", "--connect-timeout":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("wget", args)
				}
				timeout = parseSeconds(v)
			case "--quiet", "--no-verbose", "--no-check-certificate", "--content-on-error":
				// No-ops here: no progress meter, and cert verification is on.
			default:
				return fallback("wget", args)
			}
		case len(a) >= 2 && a[0] == '-' && a != "-":
			chars := a[1:]
			handled := true
			for idx := 0; idx < len(chars); idx++ {
				c := chars[idx]
				rest := chars[idx+1:]
				take := func() (string, bool) {
					if rest != "" {
						return rest, true
					}
					if i+1 >= len(args) {
						return "", false
					}
					i++
					return args[i], true
				}
				switch c {
				case 'q', 'v': // quiet / verbose — no-ops here
				case 'O':
					v, ok := take()
					if !ok {
						return fallback("wget", args)
					}
					output, haveOut = v, true
					idx = len(chars)
				case 'U':
					v, ok := take()
					if !ok {
						return fallback("wget", args)
					}
					headers["User-Agent"] = v
					idx = len(chars)
				case 'T':
					v, ok := take()
					if !ok {
						return fallback("wget", args)
					}
					timeout = parseSeconds(v)
					idx = len(chars)
				default:
					handled = false
				}
				if !handled {
					break
				}
			}
			if !handled {
				return fallback("wget", args)
			}
		default:
			if url != "" || !isHTTPURL(a) {
				return fallback("wget", args)
			}
			url = a
		}
	}

	if url == "" {
		return fallback("wget", args)
	}

	resp, err := doRequest(method, url, headers, body, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wget: %v\n", err)
		return 4
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "wget: server returned error: HTTP %d\n", resp.StatusCode)
		return 8
	}
	respBody, _ := io.ReadAll(resp.Body)

	// -O- (or a bare "-") streams to stdout; otherwise wget saves to a file.
	if haveOut && (output == "-" || output == "") {
		os.Stdout.Write(respBody)
		return 0
	}
	dest := output
	if dest == "" {
		dest = basename(url, "index.html")
	}
	if err := os.WriteFile(dest, respBody, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "wget: cannot write %s: %v\n", dest, err)
		return 3
	}
	fmt.Fprintf(os.Stderr, "saved %d bytes to %s\n", len(respBody), dest)
	return 0
}
