package live

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type WSClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewWSClient(conn *websocket.Conn) *WSClient {
	return &WSClient{conn: conn}
}

func (c *WSClient) Send(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *WSClient) Close() {
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}
