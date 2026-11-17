package bru

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
)

// A minimal RFC 6455 WebSocket client, enough to speak the Chrome DevTools
// Protocol to a local browser without extra dependencies.

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA
)

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsDial opens a ws:// connection.
func wsDial(ctx context.Context, rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported websocket scheme %q", u.Scheme)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: u.Path, RawQuery: u.RawQuery},
		Host:   u.Host,
		Header: http.Header{
			"Upgrade":               {"websocket"},
			"Connection":            {"Upgrade"},
			"Sec-WebSocket-Key":     {key},
			"Sec-WebSocket-Version": {"13"},
		},
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		conn.Close()
		return nil, fmt.Errorf("websocket handshake failed: %s", resp.Status)
	}
	return &wsConn{conn: conn, br: br}, nil
}

// writeFrame writes a single final frame. Clients must mask; servers must not.
func writeFrame(w io.Writer, opcode byte, payload []byte, mask bool) error {
	header := []byte{0x80 | opcode}
	maskBit := byte(0)
	if mask {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		header = append(header, maskBit|byte(n))
	case n <= 0xFFFF:
		header = append(header, maskBit|126, 0, 0)
		binary.BigEndian.PutUint16(header[2:], uint16(n))
	default:
		header = append(header, maskBit|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[2:], uint64(n))
	}
	data := payload
	if mask {
		key := make([]byte, 4)
		_, _ = rand.Read(key)
		header = append(header, key...)
		data = make([]byte, len(payload))
		for i, b := range payload {
			data[i] = b ^ key[i%4]
		}
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// readFrame reads one frame, unmasking it if needed.
func readFrame(r io.Reader) (fin bool, opcode byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	opcode = h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(r, ext[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > 64<<20 {
		err = errors.New("websocket frame too large")
		return
	}
	var key [4]byte
	if masked {
		if _, err = io.ReadFull(r, key[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= key[i%4]
		}
	}
	return
}

func (c *wsConn) WriteText(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.conn, wsOpText, b, true)
}

// ReadMessage returns the next text/binary message, answering pings and
// reassembling fragments. It returns io.EOF when the peer closes.
func (c *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	for {
		fin, op, payload, err := readFrame(c.br)
		if err != nil {
			return nil, err
		}
		switch op {
		case wsOpPing:
			c.wmu.Lock()
			err = writeFrame(c.conn, wsOpPong, payload, true)
			c.wmu.Unlock()
			if err != nil {
				return nil, err
			}
			continue
		case wsOpPong:
			continue
		case wsOpClose:
			return nil, io.EOF
		case wsOpText, wsOpBinary, wsOpContinuation:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		}
	}
}

func (c *wsConn) Close() error { return c.conn.Close() }
