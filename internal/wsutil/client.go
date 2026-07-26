// Package wsutil is a minimal RFC 6455 client, enough to drive the Discord
// gateway without pulling in a WebSocket dependency.
//
// Scope: text and binary data frames with fragmentation, plus ping/pong/close
// control frames. Extensions (including permessage-deflate) are not negotiated.
package wsutil

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Opcodes defined by RFC 6455.
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Close codes used by this package.
const (
	CloseNormal        = 1000
	CloseGoingAway     = 1001
	CloseProtocolErr   = 1002
	CloseMessageTooBig = 1009
)

// magicGUID is the fixed value from RFC 6455 §1.3.
const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// ErrClosed is returned once the connection has been closed.
var ErrClosed = errors.New("websocket: connection closed")

// CloseError reports why the peer closed the connection.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("websocket closed (%d): %s", e.Code, e.Reason)
}

// Conn is a client-side WebSocket connection. It is safe for one concurrent
// reader and one concurrent writer.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader

	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool

	// maxMessage bounds a reassembled message so a hostile peer cannot exhaust
	// memory.
	maxMessage int64
}

// DialOptions configures Dial.
type DialOptions struct {
	Header      http.Header
	Timeout     time.Duration
	MaxMessage  int64
	TLSConfig   *tls.Config
	Subprotocol string
}

// Dial opens a WebSocket connection to a ws:// or wss:// URL.
func Dial(rawURL string, opts *DialOptions) (*Conn, error) {
	if opts == nil {
		opts = &DialOptions{}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxMessage <= 0 {
		opts.MaxMessage = 16 << 20
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	secure := false
	switch u.Scheme {
	case "wss", "https":
		secure = true
	case "ws", "http":
	default:
		return nil, fmt.Errorf("websocket: unsupported scheme %q", u.Scheme)
	}

	host := u.Host
	if u.Port() == "" {
		if secure {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}

	dialer := &net.Dialer{Timeout: opts.Timeout}
	var conn net.Conn
	if secure {
		cfg := opts.TLSConfig
		if cfg == nil {
			cfg = &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
		}
		conn, err = tls.DialWithDialer(dialer, "tcp", host, cfg)
	} else {
		conn, err = dialer.Dial("tcp", host)
	}
	if err != nil {
		return nil, err
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		conn.Close()
		return nil, err
	}
	nonce := base64.StdEncoding.EncodeToString(key)

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&req, "Host: %s\r\n", u.Host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", nonce)
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	if opts.Subprotocol != "" {
		fmt.Fprintf(&req, "Sec-WebSocket-Protocol: %s\r\n", opts.Subprotocol)
	}
	for k, vs := range opts.Header {
		for _, v := range vs {
			fmt.Fprintf(&req, "%s: %s\r\n", k, v)
		}
	}
	req.WriteString("\r\n")

	_ = conn.SetWriteDeadline(time.Now().Add(opts.Timeout))
	if _, err := io.WriteString(conn, req.String()); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(opts.Timeout))
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("websocket handshake: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		conn.Close()
		return nil, fmt.Errorf("websocket handshake failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		conn.Close()
		return nil, errors.New("websocket: server did not upgrade")
	}
	sum := sha1.Sum([]byte(nonce + magicGUID))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		conn.Close()
		return nil, errors.New("websocket: invalid Sec-WebSocket-Accept")
	}

	// Clear handshake deadlines; the caller manages read/write deadlines.
	_ = conn.SetDeadline(time.Time{})
	return &Conn{conn: conn, br: br, maxMessage: opts.MaxMessage}, nil
}

// frame is one parsed WebSocket frame.
type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// readFrame reads a single frame. Client-bound frames must not be masked.
func (c *Conn) readFrame() (frame, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return frame{}, err
	}

	f := frame{
		fin:    head[0]&0x80 != 0,
		opcode: head[0] & 0x0F,
	}
	if head[0]&0x70 != 0 {
		return frame{}, errors.New("websocket: reserved bits set (no extension negotiated)")
	}
	masked := head[1]&0x80 != 0
	length := int64(head[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
		if length < 0 {
			return frame{}, errors.New("websocket: frame length overflow")
		}
	}
	if length > c.maxMessage {
		return frame{}, fmt.Errorf("websocket: frame of %d bytes exceeds limit", length)
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return frame{}, err
		}
	}
	if length > 0 {
		f.payload = make([]byte, length)
		if _, err := io.ReadFull(c.br, f.payload); err != nil {
			return frame{}, err
		}
		if masked {
			for i := range f.payload {
				f.payload[i] ^= maskKey[i%4]
			}
		}
	}
	return f, nil
}

// Read returns the next complete message, transparently answering pings and
// reassembling fragmented messages.
func (c *Conn) Read() (opcode byte, data []byte, err error) {
	var (
		buf      []byte
		msgType  byte
		fragment bool
	)

	for {
		f, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}

		switch f.opcode {
		case opPing:
			if err := c.write(opPong, f.payload); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			code := CloseNormal
			reason := ""
			if len(f.payload) >= 2 {
				code = int(binary.BigEndian.Uint16(f.payload[:2]))
				reason = string(f.payload[2:])
			}
			_ = c.writeClose(CloseNormal, "")
			c.shutdown()
			return 0, nil, &CloseError{Code: code, Reason: reason}

		case opText, opBinary:
			if fragment {
				return 0, nil, errors.New("websocket: new data frame during fragmented message")
			}
			msgType = f.opcode
			buf = f.payload
			if f.fin {
				return msgType, buf, nil
			}
			fragment = true

		case opContinuation:
			if !fragment {
				return 0, nil, errors.New("websocket: continuation without a start frame")
			}
			buf = append(buf, f.payload...)
			if int64(len(buf)) > c.maxMessage {
				return 0, nil, errors.New("websocket: message exceeds limit")
			}
			if f.fin {
				return msgType, buf, nil
			}

		default:
			return 0, nil, fmt.Errorf("websocket: unknown opcode 0x%X", f.opcode)
		}
	}
}

// write emits one masked frame. Clients must mask every frame they send.
func (c *Conn) write(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return ErrClosed
	}

	var header []byte
	header = append(header, 0x80|opcode) // FIN set: this package never fragments

	n := len(payload)
	switch {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n <= 0xFFFF:
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(n))
	default:
		header = append(header, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(n))
	}

	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)

	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if n > 0 {
		if _, err := c.conn.Write(masked); err != nil {
			return err
		}
	}
	return nil
}

// WriteText sends a text message.
func (c *Conn) WriteText(data []byte) error { return c.write(opText, data) }

// WriteBinary sends a binary message.
func (c *Conn) WriteBinary(data []byte) error { return c.write(opBinary, data) }

// Ping sends a ping frame.
func (c *Conn) Ping() error { return c.write(opPing, nil) }

func (c *Conn) writeClose(code int, reason string) error {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload, uint16(code))
	copy(payload[2:], reason)
	return c.write(opClose, payload)
}

// Close sends a close frame and tears down the connection.
func (c *Conn) Close(code int, reason string) error {
	if c.isClosed() {
		return nil
	}
	err := c.writeClose(code, reason)
	c.shutdown()
	return err
}

func (c *Conn) shutdown() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.conn.Close()
}

func (c *Conn) isClosed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closed
}

// SetReadDeadline bounds how long the next Read may block.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
