package intercept

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// OtherInterceptors returns the non-Chromium interceptors: the terminal
// env-override (works everywhere), and dependency-gated ones (firefox, electron,
// android, docker, jvm) that report a clear "install X" reason via Available()
// until their runtime is present. This is the "ship gated stubs first" path
// from docs/intercept-port.md — the real per-runtime work lands behind these.
func OtherInterceptors() []Interceptor {
	return []Interceptor{
		&terminalInterceptor{},
		&androidInterceptor{}, // real implementation (see android.go)
		&gatedInterceptor{
			id: "firefox", label: "Firefox", category: "browser", tool: "certutil",
			hint: "Firefox interception needs the NSS 'certutil' tool (libnss3-tools / brew install nss) to trust the CA in its profile.",
		},
		&gatedInterceptor{
			id: "electron", label: "Electron app", category: "app", tool: "",
			hint: "Electron interception attaches to a Node inspector (--inspect-brk); packaged/fused apps can't be hooked. Not yet enabled in this build.",
			always: true,
		},
		&gatedInterceptor{
			id: "docker", label: "Docker container", category: "container", tool: "docker",
			hint: "Docker interception needs the docker CLI + daemon. Attach-mode (env + CA volume on a named container) is planned; for now set HTTP_PROXY/CA env in your image.",
		},
		&gatedInterceptor{
			id: "jvm", label: "JVM process", category: "app", tool: "java",
			hint: "JVM runtime-attach needs a JDK and a prebuilt java-agent.jar (not bundled). Use terminal env (JAVA_TOOL_OPTIONS) instead — see the terminal interceptor.",
		},
	}
}

// ---- terminal env-override (works everywhere) -------------------------------

// terminalInterceptor doesn't launch anything; it hands back the environment
// exports that route a shell's HTTP(S) clients (curl, git, python, node, etc.)
// through the proxy and trust the CA. The operator pastes them into a terminal.
type terminalInterceptor struct{}

func (terminalInterceptor) ID() string       { return "terminal" }
func (terminalInterceptor) Label() string    { return "Terminal" }
func (terminalInterceptor) Category() string { return "terminal" }
func (terminalInterceptor) Available(context.Context) (bool, string) { return true, "" }

func (terminalInterceptor) Activate(_ context.Context, opts ActivateOpts) (Session, error) {
	proxy := "http://" + opts.ProxyAddr
	var b strings.Builder
	if runtime.GOOS == "windows" {
		fmt.Fprintf(&b, "set HTTP_PROXY=%s\r\n", proxy)
		fmt.Fprintf(&b, "set HTTPS_PROXY=%s\r\n", proxy)
		fmt.Fprintf(&b, "set NODE_EXTRA_CA_CERTS=%s\r\n", opts.CACertPath)
		fmt.Fprintf(&b, "set REQUESTS_CA_BUNDLE=%s\r\n", opts.CACertPath)
		fmt.Fprintf(&b, "set SSL_CERT_FILE=%s\r\n", opts.CACertPath)
		fmt.Fprintf(&b, "set GIT_SSL_CAINFO=%s\r\n", opts.CACertPath)
	} else {
		fmt.Fprintf(&b, "export HTTP_PROXY=%s\n", proxy)
		fmt.Fprintf(&b, "export HTTPS_PROXY=%s\n", proxy)
		fmt.Fprintf(&b, "export http_proxy=%s\n", proxy)
		fmt.Fprintf(&b, "export https_proxy=%s\n", proxy)
		// Per-runtime CA bundles so TLS verification still passes.
		fmt.Fprintf(&b, "export NODE_EXTRA_CA_CERTS=%q\n", opts.CACertPath)   // Node
		fmt.Fprintf(&b, "export REQUESTS_CA_BUNDLE=%q\n", opts.CACertPath)    // Python requests
		fmt.Fprintf(&b, "export SSL_CERT_FILE=%q\n", opts.CACertPath)         // OpenSSL/curl
		fmt.Fprintf(&b, "export GIT_SSL_CAINFO=%q\n", opts.CACertPath)        // git
		fmt.Fprintf(&b, "export CARGO_HTTP_CAINFO=%q\n", opts.CACertPath)     // rust/cargo
		fmt.Fprintf(&b, "export AWS_CA_BUNDLE=%q\n", opts.CACertPath)         // aws cli
		fmt.Fprintf(&b, "export DENO_CERT=%q\n", opts.CACertPath)             // deno
	}
	return &terminalSession{env: b.String()}, nil
}

type terminalSession struct{ env string }

func (terminalSession) ID() string          { return "terminal" }
func (terminalSession) Interceptor() string  { return "terminal" }
func (s terminalSession) Info() map[string]any {
	return map[string]any{"env": s.env, "instructions": "Paste these into the shell you want intercepted, then run your commands there."}
}
func (terminalSession) Stop() error { return nil }

// ---- dependency-gated stubs -------------------------------------------------

// gatedInterceptor reports unavailable (with an install hint) until its runtime
// tool is on PATH. `always` forces the hint regardless of tool presence (used
// where even a present tool isn't enough, e.g. electron needs an inspector).
type gatedInterceptor struct {
	id, label, category, tool, hint string
	always                          bool
}

func (g *gatedInterceptor) ID() string       { return g.id }
func (g *gatedInterceptor) Label() string    { return g.label }
func (g *gatedInterceptor) Category() string { return g.category }

func (g *gatedInterceptor) Available(context.Context) (bool, string) {
	if g.always {
		return false, g.hint
	}
	if g.tool != "" {
		if _, err := exec.LookPath(g.tool); err != nil {
			return false, g.hint
		}
	}
	// Tool present, but the full interception path isn't implemented yet.
	return false, g.hint
}

func (g *gatedInterceptor) Activate(context.Context, ActivateOpts) (Session, error) {
	return nil, fmt.Errorf("%s", g.hint)
}
