package sessionevent

import (
	"context"
	"errors"
	"sync"
)

// Handler 是订阅方对单个会话撤销事件的处理函数。
type Handler func(context.Context, SessionRevokedEvent) error

// Subscription 代表一个可关闭的订阅。
type Subscription interface {
	Close() error
}

// Bus 提供会话撤销事件的发布与订阅能力。
type Bus interface {
	Publish(context.Context, SessionRevokedEvent) error
	Subscribe(context.Context, string, Handler) (Subscription, error)
}

var ErrBusNotConfigured = errors.New("会话事件总线未初始化")

var defaultBus struct {
	sync.RWMutex
	bus Bus
}

// SetDefaultBus 设置进程内默认总线。应用启动后调用一次；传入 nil 可在关闭时释放引用。
func SetDefaultBus(bus Bus) {
	defaultBus.Lock()
	defer defaultBus.Unlock()
	defaultBus.bus = bus
}

// Publish 通过默认总线发布事件。
func Publish(ctx context.Context, event SessionRevokedEvent) error {
	bus := getDefaultBus()
	if bus == nil {
		return ErrBusNotConfigured
	}
	return bus.Publish(ctx, event)
}

// Subscribe 使用默认总线订阅事件。
func Subscribe(ctx context.Context, subscriberName string, handler Handler) (Subscription, error) {
	bus := getDefaultBus()
	if bus == nil {
		return nil, ErrBusNotConfigured
	}
	return bus.Subscribe(ctx, subscriberName, handler)
}

func getDefaultBus() Bus {
	defaultBus.RLock()
	defer defaultBus.RUnlock()
	return defaultBus.bus
}
