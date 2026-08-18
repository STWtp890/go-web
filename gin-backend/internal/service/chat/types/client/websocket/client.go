// Package websocket 提供 WebSocket 连接封装: WebSocketClient 实现 client.Client
package websocket

import (
	"context"
	"errors"
	"sync"
	"time"

	"gin-backend/internal/service/chat/types/client"
	msg "gin-backend/internal/service/chat/types/message"

	"github.com/gorilla/websocket"
)

type heartbeat struct {
	ctx            context.Context
	stopIn         chan struct{}
	stopOnce       sync.Once
	timeoutSecond  int32
	failedCount    int32
	maxFailedCount int32
	onStop         func() // 心跳停止回调 (连接清理, 心跳协程退出时触发)
}

func newHeartbeat(ctx context.Context, timeoutSecond int32) *heartbeat {
	return &heartbeat{
		ctx:            ctx,
		timeoutSecond:  timeoutSecond,
		stopIn:         make(chan struct{}),
		failedCount:    0,
		maxFailedCount: 3,
	}
}

// SetOnStop 注册心跳停止回调: 心跳协程退出 (ping 超时/上下文取消/主动停止) 时触发
// 用于上层执行连接清理 (如从注册表 Detach + Offline); 回调需幂等
func (h *heartbeat) SetOnStop(fn func()) {
	h.onStop = fn
}

// StopSignal 返回心跳停止信号: stop 关闭后持续可读, 供读写协程退出
func (h *heartbeat) StopSignal() <-chan struct{} {
	return h.stopIn
}

// notifyStop 触发心跳停止回调 (心跳协程仅退出一次, 天然幂等)
func (h *heartbeat) notifyStop() {
	if h.onStop != nil {
		h.onStop()
	}
}

// startListen 启动心跳协程: 定时发送 ping 帧, 维持连接活跃
func (h *heartbeat) start(conn *websocket.Conn) {
	// 启动心跳协程: 定时发送 ping 帧, 维持连接活跃
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(time.Duration(h.timeoutSecond*2) * time.Second))
	})

	go func() {
		// 心跳停止 (任意退出分支) → 触发连接清理回调
		defer h.notifyStop()
		ticker := time.NewTicker(time.Second * time.Duration(h.timeoutSecond))
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			case <-h.stopIn: // 连接已关闭
				conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				return
			case <-h.ctx.Done(): // 上下文取消
				return
			}
		}
	}()
}

// stop 停止心跳 (幂等): 关闭停止信号, 心跳/读写协程经 StopSignal 退出
// 采用 close 而非 send, 避免心跳协程已退出时 send 永久阻塞 (死锁)
func (h *heartbeat) stop() {
	h.stopOnce.Do(func() { close(h.stopIn) })
}

// WebSocketClient 封装 WebSocket 连接, 实现 client.Client 接口
// 出站: Send 入队 outCh, 单一写协程消费并写入连接 (避免并发写; 缓冲满丢弃)
// 心跳: 心跳协程退出 (ping 超时/上下文取消/主动关闭) 时触发 onStop 回调执行连接清理
type WebSocketClient struct {
	closeOnce sync.Once
	ctx       context.Context

	conn      *websocket.Conn
	heartbeat *heartbeat
	outCh     chan []byte // 出站消息队列 (Send 生产, 写协程消费)
	onStop    func()      // 连接清理回调 (bridge 注册, 心跳停止时触发)
}

// NewWebSocketClient 创建 WebSocket 客户端
func NewWebSocketClient(ctx context.Context, conn *websocket.Conn, inCh chan []byte) *WebSocketClient {
	c := &WebSocketClient{
		ctx:  ctx,
		conn: conn,
	}
	// 接入心跳: 失败时经 stopHeartbeat 幂等触发关闭, 停止信号由 Close 关闭
	c.heartbeat = newHeartbeat(ctx, 30) // 30 秒超时
	c.heartbeat.start(conn)
	// 开启读写协程: 读取消息并投递到管道, 写入消息由 Send 方法调用

	return c
}

// SetOnStop 注册连接清理回调: 心跳停止 (连接异常/超时/主动关闭) 时触发
// 回调需幂等; 典型实现: 从注册表 Detach + Offline (关闭连接)
func (c *WebSocketClient) SetOnStop(fn func()) {
	c.onStop = fn
}

// Init 启动读写协程:
//   - 读协程: conn.ReadMessage → inCh (入站消息), 读错误上报 errCh 后退出
//   - 写协程: 消费 outCh → conn.WriteMessage (出站消息, 唯一写者), 写错误上报 errCh 后退出
//
// 协程经 ctx.Done 或心跳停止信号 (StopSignal) 退出; 心跳停止时触发上层连接清理回调
func (c *WebSocketClient) Init(inCh chan []byte, outCh chan []byte, errCh chan error) {
	c.outCh = outCh
	// 心跳停止 → 触发上层连接清理回调 (心跳协程退出时执行)
	c.heartbeat.SetOnStop(c.onStop)

	// 读协程: 从连接读取消息并投递到 inCh
	go func() {
		defer close(inCh) // 读协程退出时关闭 inCh, 结束入站消费方
		for {
			_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				c.reportError(errCh, err)
				return
			}
			select {
			case inCh <- data:
			case <-c.ctx.Done():
				return
			case <-c.heartbeat.StopSignal():
				return
			}
		}
	}()

	// 写协程: 消费 outCh 并写入连接
	go func() {
		for {
			select {
			case data := <-outCh:
				if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
					c.reportError(errCh, err)
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

// reportError 上报错误 (非阻塞, 缓冲满丢弃)
func (c *WebSocketClient) reportError(errCh chan error, err error) {
	if err == nil {
		return
	}
	select {
	case errCh <- err:
	default:
	}
}

// Close 关闭连接 (幂等): 停止心跳并关闭底层连接
func (c *WebSocketClient) Close() error {
	c.closeOnce.Do(func() {
		c.heartbeat.stop()
		c.conn.Close()
	})
	return nil
}

// sendWaitTimeout 出站缓冲满时的等待时长 (背压: 等写协程腾出空间, 超时后丢弃)
const sendWaitTimeout = 200 * time.Millisecond

// Send 非阻塞出站投递: 序列化为 wire 格式 JSON 入队 outCh
// 缓冲满时短暂等待 (背压), 超时后丢弃; 返回 nil (不触发 Online 下线)
// 由写协程统一写入连接; 真实写错误由写协程上报 (Send 不感知)
func (c *WebSocketClient) Send(m msg.Message) error {
	select {
	case c.outCh <- m.Marshal():
		return nil
	case <-time.After(sendWaitTimeout):
		return errors.New("websocket 出站队列已满")
	}
}

// 编译期断言: WebSocketClient 实现 client.Client
var _ client.Client = (*WebSocketClient)(nil)
