package sessionevent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// MemoryBus 是单进程开发与单元测试可用的总线实现。
type MemoryBus struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers map[uint64]Handler
}

// NewMemoryBus 创建内存总线。
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{handlers: make(map[uint64]Handler)}
}

// Publish 异步执行每个已注册处理器；处理器失败只记录日志，不影响其他订阅者。
func (b *MemoryBus) Publish(ctx context.Context, event SessionRevokedEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.handlers))
	for _, handler := range b.handlers {
		handlers = append(handlers, handler)
	}
	b.mu.RUnlock()

	for _, handler := range handlers {
		go invokeHandler(ctx, handler, event)
	}
	return nil
}

// Subscribe 注册内存订阅。subscriberName 仅用于保持和分布式实现一致的接口。
func (b *MemoryBus) Subscribe(ctx context.Context, subscriberName string, handler Handler) (Subscription, error) {
	if subscriberName == "" || handler == nil {
		return nil, errors.New("订阅名称和处理器不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.handlers[id] = handler
	b.mu.Unlock()

	sub := &memorySubscription{bus: b, id: id}
	go func() {
		<-ctx.Done()
		_ = sub.Close()
	}()
	return sub, nil
}

type memorySubscription struct {
	bus  *MemoryBus
	id   uint64
	once sync.Once
}

func (s *memorySubscription) Close() error {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.handlers, s.id)
		s.bus.mu.Unlock()
	})
	return nil
}

func invokeHandler(ctx context.Context, handler Handler, event SessionRevokedEvent) {
	if err := handler(ctx, event); err != nil {
		slog.Error("session_event_handler_failed", slog.String("event_id", event.EventID), slog.String("error", err.Error()))
	}
}
