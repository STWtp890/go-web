// 群业务域: 通用泛型滑动窗口队列 (群最近消息窗口用)
package group

import "sync"

// SafeQueue 并发安全的滑动窗口队列 (泛型)
// 群聊消息队列使用: 入队采用滑动窗口 (ForcePushEnqueue), 超容量挤掉最旧;
// 读取采用 Snapshot (不弹出), 保证多成员各自补发不互相影响
type SafeQueue[T any] struct {
	items []T
	lock  sync.Mutex
}

// NewSafeQueue 创建容量为 capacity 的滑动窗口队列
func NewSafeQueue[T any](capacity int) *SafeQueue[T] {
	return &SafeQueue[T]{items: make([]T, 0, capacity)}
}

// Enqueue 将元素入队
func (q *SafeQueue[T]) Enqueue(item T) {
	q.lock.Lock()
	defer q.lock.Unlock()
	q.items = append(q.items, item)
}

// Dequeue 将元素出队
func (q *SafeQueue[T]) Dequeue() (T, bool) {
	q.lock.Lock()
	defer q.lock.Unlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// Len 返回队列长度
func (q *SafeQueue[T]) Len() int {
	q.lock.Lock()
	defer q.lock.Unlock()
	return len(q.items)
}

// ForcePushEnqueue 滑动窗口入队: 队列满时挤掉最旧元素并返回被挤出的元素
func (q *SafeQueue[T]) ForcePushEnqueue(item T) (T, bool) {
	q.lock.Lock()
	defer q.lock.Unlock()
	var pushOut T
	var ok bool
	if len(q.items) < cap(q.items) {
		q.items = append(q.items, item)
	} else {
		pushOut = q.items[0]
		q.items = q.items[1:]
		q.items = append(q.items, item)
		ok = true
	}
	return pushOut, ok
}

// Snapshot 返回队列元素副本 (不弹出, 供群聊多成员补发最近消息)
func (q *SafeQueue[T]) Snapshot() []T {
	q.lock.Lock()
	defer q.lock.Unlock()
	out := make([]T, len(q.items))
	copy(out, q.items)
	return out
}

// Replace 以给定序列重建队列内容 (群历史懒加载恢复窗口用)
func (q *SafeQueue[T]) Replace(items []T) {
	q.lock.Lock()
	defer q.lock.Unlock()
	q.items = make([]T, 0, cap(q.items))
	q.items = append(q.items, items...)
}
