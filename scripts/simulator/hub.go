package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Client represents a connected WebSocket browser client.
type Client struct {
	hub  *Hub
	conn net.Conn
	send chan []byte
}

// Hub maintains the set of active WebSocket clients and broadcasts events.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.Mutex
	done       chan struct{}
}

// NewHub creates a new WebSocket Hub.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
	}
}

// Run starts the hub loop for registering, unregistering, and broadcasting.
func (h *Hub) Run() {
	for {
		select {
		case <-h.done:
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
				_ = client.conn.Close()
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.Unlock()
		}
	}
}

// Stop terminates the hub.
func (h *Hub) Stop() {
	close(h.done)
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
	}
}

// ClientCount returns the number of active connected clients.
func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// ServeWS handles WebSocket upgrade requests and registers the client.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
		return
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}

	// Compute Sec-WebSocket-Accept
	hSha := sha1.New()
	hSha.Write([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(hSha.Sum(nil))

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}

	conn, bufrw, err := hijacker.Hijack()
	if err != nil {
		log.Printf("[simulator-ws] hijack error: %v", err)
		return
	}

	// Send handshake response
	resp := fmt.Sprintf("HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Accept: %s\r\n\r\n", accept)
	if _, err := bufrw.WriteString(resp); err != nil {
		_ = conn.Close()
		return
	}
	if err := bufrw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 64),
	}

	h.register <- client

	go client.writePump()
	go client.readPump(bufrw.Reader)
}

func (c *Client) readPump(r *bufio.Reader) {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(r, header); err != nil {
			return
		}

		opcode := header[0] & 0x0F
		if opcode == 0x8 { // Close frame
			return
		}

		isMasked := (header[1] & 0x80) != 0
		payloadLen := int(header[1] & 0x7F)

		if payloadLen == 126 {
			ext := make([]byte, 2)
			if _, err := io.ReadFull(r, ext); err != nil {
				return
			}
			payloadLen = int(binary.BigEndian.Uint16(ext))
		} else if payloadLen == 127 {
			ext := make([]byte, 8)
			if _, err := io.ReadFull(r, ext); err != nil {
				return
			}
			payloadLen = int(binary.BigEndian.Uint64(ext))
		}

		var mask [4]byte
		if isMasked {
			if _, err := io.ReadFull(r, mask[:]); err != nil {
				return
			}
		}

		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}

		// Client messages are ignored in this version; client is receive-only.
	}
}

func (c *Client) writePump() {
	defer func() {
		_ = c.conn.Close()
	}()

	for msg := range c.send {
		frame, err := encodeTextFrame(msg)
		if err != nil {
			return
		}
		if _, err := c.conn.Write(frame); err != nil {
			return
		}
	}
}

func encodeTextFrame(payload []byte) ([]byte, error) {
	length := len(payload)
	var header []byte

	if length < 126 {
		header = []byte{0x81, byte(length)}
	} else if length <= 65535 {
		header = []byte{0x81, 126, 0, 0}
		binary.BigEndian.PutUint16(header[2:], uint16(length))
	} else {
		header = make([]byte, 10)
		header[0] = 0x81
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:], uint64(length))
	}

	if len(header)+length < length {
		return nil, errors.New("frame payload overflow")
	}

	frame := append(header, payload...)
	return frame, nil
}
