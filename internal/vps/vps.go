// Package vps connects to a user's server over SSH and reads its state on
// demand — no agent installed on the box, just standard commands whose output
// is parsed into metrics. It also runs arbitrary commands for the VPS-manager
// tool.
package vps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Target is everything needed to dial one host.
type Target struct {
	Host       string
	Port       int
	Username   string
	AuthMethod string // password|key
	Password   string
	PrivateKey string
	Passphrase string
	// KnownHostKey is the server's pinned SSH public key (authorized_keys format,
	// e.g. "ssh-ed25519 AAAA..."). Empty means first use: any key is accepted and
	// returned via the connection's SeenHostKey for the caller to pin. Non-empty
	// means the presented key MUST match, or the dial fails — this is what stops
	// a MITM from harvesting the credentials.
	KnownHostKey string
}

func (t Target) addr() string {
	p := t.Port
	if p == 0 {
		p = 22
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(p))
}

// ErrHostKeyChanged is returned when a host presents a key different from the
// pinned one — a possible man-in-the-middle, or a legitimately rebuilt server.
var ErrHostKeyChanged = errors.New("host key changed since it was first trusted — possible MITM, or the server was rebuilt; remove and re-add it if you trust the change")

// conn wraps an ssh.Client with the host key the server actually presented, so
// the caller can pin it after a first-use connect.
type conn struct {
	*ssh.Client
	seenHostKey string
}

// dial opens an SSH client with trust-on-first-use host-key verification. On
// first use (t.KnownHostKey empty) it records the key; thereafter it must match.
func dial(ctx context.Context, t Target) (*conn, error) {
	auth, err := authMethods(t)
	if err != nil {
		return nil, err
	}
	user := t.Username
	if user == "" {
		user = "root"
	}

	var seen string
	hostKeyCb := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seen = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if known := strings.TrimSpace(t.KnownHostKey); known != "" && !hostKeysEqual(known, seen) {
			return ErrHostKeyChanged
		}
		return nil
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hostKeyCb,
		Timeout:         12 * time.Second,
	}
	d := net.Dialer{Timeout: 12 * time.Second}
	netConn, err := d.DialContext(ctx, "tcp", t.addr())
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", t.addr(), err)
	}
	c, chans, reqs, err := ssh.NewClientConn(netConn, t.addr(), cfg)
	if err != nil {
		netConn.Close()
		// A host-key mismatch surfaces here wrapped by the ssh handshake; keep the
		// sentinel recognisable to the caller.
		if errors.Is(err, ErrHostKeyChanged) {
			return nil, ErrHostKeyChanged
		}
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return &conn{Client: ssh.NewClient(c, chans, reqs), seenHostKey: seen}, nil
}

// hostKeysEqual compares two authorized_keys lines by their type+base64 body,
// ignoring any trailing comment.
func hostKeysEqual(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	if len(fa) < 2 || len(fb) < 2 {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return fa[0] == fb[0] && fa[1] == fb[1]
}

func authMethods(t Target) ([]ssh.AuthMethod, error) {
	if t.AuthMethod == "key" || (t.AuthMethod == "" && t.PrivateKey != "") {
		key := strings.TrimSpace(t.PrivateKey)
		if key == "" {
			return nil, fmt.Errorf("auth method is key but no private key was provided")
		}
		var signer ssh.Signer
		var err error
		if t.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(key), []byte(t.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(key))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	if t.Password == "" {
		return nil, fmt.Errorf("no password or private key configured")
	}
	return []ssh.AuthMethod{ssh.Password(t.Password)}, nil
}

// Run opens a connection, runs one command, and returns its combined output
// plus the host key the server presented (for TOFU pinning). A non-zero exit is
// returned with the output so the caller sees stderr rather than a bare error.
func Run(ctx context.Context, t Target, command string) (string, string, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", "", err
	}
	defer client.Close()
	out, err := runOn(ctx, client.Client, command)
	return out, client.seenHostKey, err
}

func runOn(ctx context.Context, client *ssh.Client, command string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		out, runErr = sess.CombinedOutput(command)
		close(done)
	}()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return string(out), ctx.Err()
	case <-done:
		return string(out), runErr
	}
}

// Process is one row from the remote process table.
type Process struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	Command string  `json:"command"`
}

// Processes lists the running processes on a host, busiest CPU first, plus the
// host key seen. Runs a single ps over one connection.
func Processes(ctx context.Context, t Target) ([]Process, string, error) {
	out, seen, err := Run(ctx, t, `ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu --no-headers 2>/dev/null | head -n 300`)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, seen, err
	}
	var procs []Process
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 5 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		cpu, _ := strconv.ParseFloat(f[2], 64)
		mem, _ := strconv.ParseFloat(f[3], 64)
		procs = append(procs, Process{
			PID: pid, User: f[1], CPU: cpu, Mem: mem,
			Command: strings.Join(f[4:], " "),
		})
	}
	return procs, seen, nil
}

// Ping confirms a host is reachable and authenticates, returning the remote
// user@hostname it landed on (proof it really connected) plus the host key seen.
func Ping(ctx context.Context, t Target) (who string, seenHostKey string, err error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", "", err
	}
	defer client.Close()
	out, err := runOn(ctx, client.Client, "whoami; hostname")
	if err != nil {
		return "", client.seenHostKey, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) >= 2 {
		return fields[0] + "@" + fields[1], client.seenHostKey, nil
	}
	return strings.TrimSpace(out), client.seenHostKey, nil
}
