// Package socialimap provides IMAP connection testing and inbox reading for
// the Social Media feature. Gmail is the primary target (imap.gmail.com:993
// with an App Password), but any IMAP server works.
package socialimap

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// Config holds the IMAP connection parameters. Password is plaintext only at
// the boundary; callers encrypt it before persistence.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
}

// DefaultGmail returns sensible Gmail defaults.
func DefaultGmail() Config {
	return Config{Host: "imap.gmail.com", Port: 993}
}

func (c Config) addr() string {
	port := c.Port
	if port == 0 {
		port = 993
	}
	return fmt.Sprintf("%s:%d", c.Host, port)
}

func (c Config) dial() (*client.Client, error) {
	nd := &net.Dialer{Timeout: 15 * time.Second}
	td := &tls.Dialer{NetDialer: nd, Config: &tls.Config{ServerName: c.Host}}
	conn, err := td.Dial("tcp", c.addr())
	if err != nil {
		return nil, fmt.Errorf("cannot connect to %s: %w", c.addr(), err)
	}
	cli, err := client.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("IMAP handshake failed: %w", err)
	}
	return cli, nil
}

// Test connects, logs in, and returns the inbox message count. It does not
// persist anything.
func (c Config) Test() (int, error) {
	cli, err := c.dial()
	if err != nil {
		return 0, err
	}
	defer cli.Logout()

	if err := cli.Login(c.Username, c.Password); err != nil {
		return 0, fmt.Errorf("login failed: %w", err)
	}

	mbox, err := cli.Select("INBOX", true)
	if err != nil {
		return 0, fmt.Errorf("cannot select inbox: %w", err)
	}
	return int(mbox.Messages), nil
}

// Message is a simplified inbox message for verification/OTP extraction.
type Message struct {
	Subject string
	From    string
	Date    time.Time
	Snippet string
}

// FetchRecent returns the n most recent messages from INBOX.
func (c Config) FetchRecent(n int) ([]Message, error) {
	if n <= 0 {
		n = 10
	}
	cli, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer cli.Logout()

	if err := cli.Login(c.Username, c.Password); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	mbox, err := cli.Select("INBOX", true)
	if err != nil {
		return nil, err
	}

	total := int(mbox.Messages)
	if total == 0 {
		return nil, nil
	}

	start := uint32(1)
	if total > n {
		start = uint32(total - n + 1)
	}

	seqset := &imap.SeqSet{}
	seqset.AddRange(start, uint32(total))

	messages := make(chan *imap.Message, n)
	section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.TextSpecifier}}
	items := []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}

	done := make(chan error, 1)
	go func() {
		done <- cli.Fetch(seqset, items, messages)
	}()

	var out []Message
	for msg := range messages {
		var m Message
		if msg.Envelope != nil {
			m.Subject = msg.Envelope.Subject
			if len(msg.Envelope.From) > 0 {
				m.From = msg.Envelope.From[0].Address()
			}
			m.Date = msg.Envelope.Date
		}
		if lit := msg.GetBody(section); lit != nil {
			data, _ := io.ReadAll(io.LimitReader(lit, 4096))
			m.Snippet = strings.TrimSpace(string(data))
		}
		out = append(out, m)
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return out, nil
}
