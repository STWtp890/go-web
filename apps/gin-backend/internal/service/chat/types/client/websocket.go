// Package client 定义聊天连接抽象与 WebSocket 传输实现。
package client

import (
	"context"
	"errors"
	"sync"
	"time"

	"gin-backend/internal/service/chat/types/message"

	"github.com/gorilla/websocket"
)

type heartbeat struct {
	ctx           context.Context
	stopIn        chan struct{}
	stopOnce      sync.Once
	timeoutSecond int32
	onStop        func()
}

func newHeartbeat(ctx context.Context, timeoutSecond int32) *heartbeat {
	return &heartbeat{
		ctx:           ctx,
		timeoutSecond: timeoutSecond,
		stopIn:        make(chan struct{}),
	}
}

// SetOnStop 注册心跳停止回调；回调应保证幂等。
func (h *heartbeat) SetOnStop(fn func()) {
	h.onStop = fn
}

// StopSignal 返回连接停止信号，供读写协程退出。
func (h *heartbeat) StopSignal() <-chan struct{} {
	return h.stopIn
}

func (h *heartbeat) notifyStop() {
	if h.onStop != nil {
		h.onStop()
	}
}

// start 启动 Ping/Pong 心跳；任意退出分支最终触发连接清理回调。
func (h *heartbeat) start(conn *websocket.Conn) {
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(time.Duration(h.timeoutSecond*2) * time.Second))
	})

	go func() {
		defer h.notifyStop()
		ticker := time.NewTicker(time.Second * time.Duration(h.timeoutSecond))
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			case <-h.stopIn:
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				return
			case <-h.ctx.Done():
				return
			}
		}
	}()
}

func (h *heartbeat) stop() {
	h.stopOnce.Do(func() { close(h.stopIn) })
}

// WebSocketClient 使用 Gorilla WebSocket 实现 Connection。
// 单一写协程避免并发写；有界出站队列提供背压；心跳退出触发统一连接清理。
type WebSocketClient struct {
	closeOnce sync.Once
	startOnce sync.Once
	ctx       context.Context

	conn      *websocket.Conn
	heartbeat *heartbeat
	inCh      chan []byte
	outCh     chan []byte
}

// NewWebSocketClient 创建尚未启动读写协程的 WebSocket 连接适配器。
func NewWebSocketClient(ctx context.Context, conn *websocket.Conn) *WebSocketClient {
	c := &WebSocketClient{
		ctx:   ctx,
		conn:  conn,
		inCh:  make(chan []byte, 64),
		outCh: make(chan []byte, 64),
	}
	c.heartbeat = newHeartbeat(ctx, 30)
	return c
}

// Start 启动读写与心跳协程，重复调用不会重复启动。
func (c *WebSocketClient) Start(onStop func()) {
	c.startOnce.Do(func() {
		c.heartbeat.SetOnStop(onStop)
		c.heartbeat.start(c.conn)
		c.startReadPump()
		c.startWritePump()
	})
}

// Incoming 返回连接的入站消息流；读协程退出后该通道关闭。
func (c *WebSocketClient) Incoming() <-chan []byte { return c.inCh }

func (c *WebSocketClient) startReadPump() {
	go func() {
		defer close(c.inCh)
		for {
			_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				_ = c.Close()
				return
			}
			select {
			case c.inCh <- data:
			case <-c.ctx.Done():
				return
			case <-c.heartbeat.StopSignal():
				return
			}
		}
	}()
}

func (c *WebSocketClient) startWritePump() {
	go func() {
		for {
			select {
			case data := <-c.outCh:
				if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
					_ = c.Close()
					return
				}
			case <-c.ctx.Done():
				return
			case <-c.heartbeat.StopSignal():
				return
			}
		}
	}()
}

// Close 幂等停止心跳并关闭底层 WebSocket。
func (c *WebSocketClient) Close() error {
	c.closeOnce.Do(func() {
		c.heartbeat.stop()
		_ = c.conn.Close()
	})
	return nil
}

const sendWaitTimeout = 200 * time.Millisecond

// Send 将消息放入有界出站队列；背压超时由上层关闭连接。
func (c *WebSocketClient) Send(m message.Message) error {
	select {
	case c.outCh <- m.Marshal():
		return nil
	case <-time.After(sendWaitTimeout):
		return errors.New("websocket 出站队列已满")
	}
}

var _ Client = (*WebSocketClient)(nil)
var _ Connection = (*WebSocketClient)(nil)
