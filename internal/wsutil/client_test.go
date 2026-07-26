package wsutil

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// serveEcho runs a bare-bones WebSocket server that echoes text messages.
// It exercises the handshake, masking, fragmentation, and control frames.
func serveEcho(t *testing.T) (addr string, done chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done = make(chan struct{})

	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer ln.Close()

		br := bufio.NewReader(conn)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		key := req.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + magicGUID))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: "+accept+"\r\n\r\n")

		for {
			op, payload, err := readServerFrame(br)
			if err != nil {
				return
			}
			switch op {
			case opClose:
				writeServerFrame(conn, opClose, payload)
				return
			case opPing:
				writeServerFrame(conn, opPong, payload)
			case opText:
				// Echo back split across two fragments to exercise reassembly.
				if len(payload) > 4 {
					half := len(payload) / 2
					writeServerFrameFin(conn, opText, payload[:half], false)
					writeServerFrameFin(conn, opContinuation, payload[half:], true)
					continue
				}
				writeServerFrame(conn, opText, payload)
			}
		}
	}()

	return ln.Addr().String(), done
}

func readServerFrame(br *bufio.Reader) (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		return 0, nil, err
	}
	op := head[0] & 0x0F
	masked := head[1]&0x80 != 0
	n := int64(head[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		io.ReadFull(br, ext[:])
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		io.ReadFull(br, ext[:])
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(br, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return op, payload, nil
}

func writeServerFrame(w io.Writer, op byte, payload []byte) {
	writeServerFrameFin(w, op, payload, true)
}

// Server frames are never masked.
func writeServerFrameFin(w io.Writer, op byte, payload []byte, fin bool) {
	first := op
	if fin {
		first |= 0x80
	}
	header := []byte{first}
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n <= 0xFFFF:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(n))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(n))
	}
	w.Write(header)
	w.Write(payload)
}

func TestDialAndEcho(t *testing.T) {
	addr, done := serveEcho(t)

	c, err := Dial("ws://"+addr+"/gateway", &DialOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	msg := strings.Repeat("antares ", 40)
	if err := c.WriteText([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	op, data, err := c.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if op != opText {
		t.Fatalf("opcode = %d, want text", op)
	}
	if string(data) != msg {
		t.Fatalf("fragmented echo mismatch:\n got %q\nwant %q", data, msg)
	}

	if err := c.Close(CloseNormal, "bye"); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestLargeFrame(t *testing.T) {
	addr, _ := serveEcho(t)
	c, err := Dial("ws://"+addr, &DialOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(CloseNormal, "")

	// Long enough to require the 16-bit extended length path.
	msg := strings.Repeat("x", 70000)
	if err := c.WriteText([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := c.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) != len(msg) {
		t.Fatalf("got %d bytes, want %d", len(data), len(msg))
	}
}

func TestRejectsBadAccept(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		http.ReadRequest(br)
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: wrong\r\n\r\n")
	}()
	defer ln.Close()

	if _, err := Dial("ws://"+ln.Addr().String(), nil); err == nil {
		t.Fatal("expected the handshake to be rejected")
	}
}
