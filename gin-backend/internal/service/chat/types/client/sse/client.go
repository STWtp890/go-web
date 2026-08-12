// Package sse 提供 SSE 连接封装: SSEClient 实现 client.Client
package sse

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gin-backend/internal/service/chat/types/client"
	"gin-backend/internal/service/chat/types/constant"

	msg "gin-backend/internal/service/chat/types/message"
)

// eventStream 负责将 SSE 帧写入 ResponseWriter 并 Flush
// Write 持有写锁, 保证消息帧与心跳帧并发安全
type eventStream struct {
	closeOnce sync.Once
	writeMu   sync.Mutex

	w       http.ResponseWriter
	flusher http.Flusher
}

// NewEventStream 创建 SSE 出站流并设置必要响应头
func newEventStream(w http.ResponseWriter) (*eventStream, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("sse: ResponseWriter 不支持 Flush")
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no") // 禁用反向代理缓冲
	return &eventStream{
		w:       w,
		flusher: flusher,
	}, nil
}

// write 写入 SSE 帧并刷新; 写锁保证与心跳帧并发安全
func (s *eventStream) write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.w.Write(data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// Close 标记流结束 (幂等)
func (s *eventStream) close() {
	s.closeOnce.Do(func() {})
}

// EncodeEvent 将消息编码为 SSE 帧:
//
//	event: <消息类型>
//	data: <JSON 字节流>
//	(空行)
func encodeEvent(m msg.Message) ([]byte, error) {
	data := m.Marshal()
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", m.Type(), data)), nil
}

// SSEClient 封装一个 SSE 连接 (实现 client.Client)
// 心跳: startHeartbeat 定时写注释帧保活, Close/上下文取消/写失败时退出
// 心跳帧经 eventStream 写锁写入, 与消息帧并发安全
// 心跳语义: 注释帧不触发客户端事件, 仅维持连接/代理超时保活
// 若消息与心跳共用一个写锁, 心跳不会打断消息帧 (写锁保证原子性)
type SSEClient struct {
	closeOnce sync.Once
	ctx       context.Context

	stream        *eventStream
	stopHeartbeat chan struct{} // 关闭后心跳协程退出
	onStop        func()        // 心跳停止回调 (连接清理, 心跳协程退出时触发)
}

// SetOnStop 注册心跳停止回调: 心跳协程退出 (写失败/上下文取消/主动停止) 时触发
// 用于上层执行连接清理 (如从注册表 Detach + Offline); 回调需幂等
func (c *SSEClient) SetOnStop(fn func()) {
	c.onStop = fn
}

// notifyStop 触发心跳停止回调 (心跳协程仅退出一次, 天然幂等)
func (c *SSEClient) notifyStop() {
	if c.onStop != nil {
		c.onStop()
	}
}

// NewSSEClient 创建 SSE 客户端 (启动心跳保活)
func NewSSEClient(ctx context.Context, w http.ResponseWriter) (*SSEClient, error) {
	stream, err := newEventStream(w)
	if err != nil {
		return nil, err
	}
	c := &SSEClient{
		ctx:           ctx,
		stream:        stream,
		stopHeartbeat: make(chan struct{}),
	}
	c.startHeartbeat()
	return c, nil
}

// startHeartbeat 启动心跳协程: 按 HeartbeatInterval 间隔写入注释帧
// 退出条件: Close 关闭 stopHeartbeat / 上下文取消 / 写入失败 (连接断开)
func (c *SSEClient) startHeartbeat() {
	go func() {
		// 心跳停止 (任意退出分支) → 触发连接清理回调
		defer c.notifyStop()
		ticker := time.NewTicker(constant.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopHeartbeat:
				return
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				if err := c.stream.write([]byte(constant.HeartbeatFrame + "\n\n")); err != nil {
					return
				}
			}
		}
	}()
}

// Close 关闭连接 (幂等): 停止心跳协程并标记流结束
func (c *SSEClient) Close() error {
	c.closeOnce.Do(func() {
		close(c.stopHeartbeat)
		c.stream.close()
	})
	return nil
}

// Send 出站投递: 编码为 SSE 帧并写入流
func (c *SSEClient) Send(m msg.Message) error {
	frame, err := encodeEvent(m)
	if err != nil {
		return err
	}
	return c.stream.write(frame)
}

// 编译期断言: SSEClient 实现 client.Client
var _ client.Client = (*SSEClient)(nil)
