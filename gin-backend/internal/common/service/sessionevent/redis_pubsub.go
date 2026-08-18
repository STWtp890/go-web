package sessionevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	goredis "github.com/redis/go-redis/v9"
)

const redisChannel = "gin-backend:session:revoked"

// RedisPubSubBus 使用 Redis Pub/Sub 向所有应用实例广播会话撤销事件。
// Pub/Sub 为实时、非持久化传输；它不参与 Token 有效性判定。
type RedisPubSubBus struct {
	client *goredis.Client
}

// NewRedisPubSubBus 创建 Redis Pub/Sub 总线。
func NewRedisPubSubBus(client *goredis.Client) (*RedisPubSubBus, error) {
	if client == nil {
		return nil, errors.New("redis client 不能为空")
	}
	return &RedisPubSubBus{client: client}, nil
}

// Publish 序列化并发布会话撤销事件。
func (b *RedisPubSubBus) Publish(ctx context.Context, event SessionRevokedEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("序列化会话撤销事件失败: %w", err)
	}
	return b.client.Publish(ctx, redisChannel, payload).Err()
}

// Subscribe 订阅 Redis 广播并异步调用处理器。订阅关闭由返回值或 ctx 取消控制。
func (b *RedisPubSubBus) Subscribe(ctx context.Context, subscriberName string, handler Handler) (Subscription, error) {
	if subscriberName == "" || handler == nil {
		return nil, errors.New("订阅名称和处理器不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pubsub := b.client.Subscribe(ctx, redisChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, fmt.Errorf("订阅会话撤销事件失败: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := &redisSubscription{pubsub: pubsub, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(sub.done)
		defer cancel()
		for {
			msg, err := pubsub.ReceiveMessage(subCtx)
			if err != nil {
				if subCtx.Err() == nil {
					slog.Error("session_event_subscription_failed", slog.String("subscriber", subscriberName), slog.String("error", err.Error()))
				}
				return
			}

			var event SessionRevokedEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				slog.Warn("session_event_decode_failed", slog.String("subscriber", subscriberName), slog.String("error", err.Error()))
				continue
			}
			if err := event.Validate(); err != nil {
				slog.Warn("session_event_invalid", slog.String("subscriber", subscriberName), slog.String("error", err.Error()))
				continue
			}
			go invokeHandler(subCtx, handler, event)
		}
	}()
	return sub, nil
}

type redisSubscription struct {
	pubsub *goredis.PubSub
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (s *redisSubscription) Close() error {
	var err error
	s.once.Do(func() {
		s.cancel()
		err = s.pubsub.Close()
		<-s.done
	})
	return err
}
