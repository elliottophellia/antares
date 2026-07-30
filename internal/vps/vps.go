// Package vps connects to a user's server over SSH and reads its state on
// demand — no agent installed on the box, just standard commands whose output
// is parsed into metrics. It also runs arbitrary commands for the VPS-manager
// tool.
package vps

import (
	"context"
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
}

func (t Target) addr() string {
	p := t.Port
	if p == 0 {
		p = 22
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(p))
}

// dial opens an SSH client. The host key is accepted without checking — these
// are the user's own servers, added deliberately; a TOFU store would be a
// future hardening, not a correctness issue here.
func dial(ctx context.Context, t Target) (*ssh.Client, error) {
	auth, err := authMethods(t)
	if err != nil {
		return nil, err
	}
	user := t.Username
	if user == "" {
		user = "root"
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         12 * time.Second,
	}
	d := net.Dialer{Timeout: 12 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", t.addr())
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", t.addr(), err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, t.addr(), cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return ssh.NewClient(c, chans, reqs), nil
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

// Run opens a connection, runs one command, and returns its combined output.
// Used by the vps_run tool. A non-zero exit is returned with the output so the
// caller sees stderr rather than a bare error.
func Run(ctx context.Context, t Target, command string) (string, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return runOn(ctx, client, command)
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

// Processes lists the running processes on a host, busiest CPU first. Runs a
// single ps over one connection.
func Processes(ctx context.Context, t Target) ([]Process, error) {
	out, err := Run(ctx, t, `ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu --no-headers 2>/dev/null | head -n 300`)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
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
	return procs, nil
}

// Ping confirms a host is reachable and authenticates, returning the remote
// user@hostname it landed on (proof it really connected).
func Ping(ctx context.Context, t Target) (string, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return "", err
	}
	defer client.Close()
	out, err := runOn(ctx, client, "whoami; hostname")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) >= 2 {
		return fields[0] + "@" + fields[1], nil
	}
	return strings.TrimSpace(out), nil
}
