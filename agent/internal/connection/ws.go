package connection

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Message struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type CommandHandler func(cmdType string, payload json.RawMessage) (interface{}, error)

type Client struct {
	url        string
	hostID     string
	apiKey     string
	conn       *websocket.Conn
	mu         sync.Mutex
	sendCh     chan []byte
	handler    CommandHandler
	done       chan struct{}
	connected  bool
}

func NewClient(apiURL, hostID, apiKey string) *Client {
	// Build WebSocket URL
	wsURL := apiURL
	if len(wsURL) > 5 && wsURL[:5] == "https" {
		wsURL = "wss" + wsURL[5:]
	} else if len(wsURL) > 4 && wsURL[:4] == "http" {
		wsURL = "ws" + wsURL[4:]
	}
	wsURL += "/internal/agent/ws?host_id=" + hostID

	return &Client{
		url:    wsURL,
		hostID: hostID,
		apiKey: apiKey,
		sendCh: make(chan []byte, 64),
		done:   make(chan struct{}),
	}
}

func (c *Client) SetHandler(h CommandHandler) {
	c.handler = h
}

func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Run starts the connection with auto-reconnect. Blocks until done is closed.
func (c *Client) Run() {
	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-c.done:
			return
		default:
		}

		err := c.connect()
		if err != nil {
			log.Printf("[agent] connection failed: %v, retrying in %v", err, backoff)
			jitter := time.Duration(rand.Int63n(int64(time.Second)))
			time.Sleep(backoff + jitter)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Connected — reset backoff
		backoff = time.Second
		log.Printf("[agent] connected to %s", c.url)

		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()

		// Run read/write loops (blocks until disconnect)
		c.runLoops()

		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()

		log.Printf("[agent] disconnected, will reconnect...")
	}
}

func (c *Client) Close() {
	close(c.done)
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
}

func (c *Client) Send(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case c.sendCh <- data:
		return nil
	default:
		return fmt.Errorf("send channel full")
	}
}

func (c *Client) connect() error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.apiKey)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(c.url, header)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	return nil
}

func (c *Client) runLoops() {
	readDone := make(chan struct{})

	// Read loop
	go func() {
		defer close(readDone)
		for {
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				return
			}
			var msg Message
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("[agent] invalid message: %v", err)
				continue
			}
			go c.handleMessage(msg) // handle concurrently so long commands don't block reads
		}
	}()

	// Write loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case data := <-c.sendCh:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-readDone:
			return
		case <-c.done:
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

func (c *Client) handleMessage(msg Message) {
	if c.handler == nil {
		log.Printf("[agent] no handler for message type: %s", msg.Type)
		return
	}

	// Execute command
	result, err := c.handler(msg.Type, msg.Payload)

	// Send result back
	var response Message
	response.ID = msg.ID
	if err != nil {
		response.Type = msg.Type + "_error"
		errPayload, _ := json.Marshal(map[string]string{"error": err.Error()})
		response.Payload = errPayload
	} else {
		response.Type = msg.Type + "_result"
		response.Payload, _ = json.Marshal(result)
	}

	if err := c.Send(response); err != nil {
		log.Printf("[agent] failed to send result: %v", err)
	}
}
