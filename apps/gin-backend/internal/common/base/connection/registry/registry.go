// Package registry 提供连接注册管理的核心原语: 注册 / 注销 / 获取。
// 业务通过 serviceName 将各自的连接实例注册到注册表, 经 Get 获取, 经 Unregister 注销释放。
package registry

import (
	"errors"
	"fmt"
	"sync"
)

// 预定义错误
var (
	// ErrAlreadyRegistered 连接重复注册
	ErrAlreadyRegistered = errors.New("registry: 连接已注册")
	// ErrNotRegistered 连接未注册
	ErrNotRegistered = errors.New("registry: 连接未注册")
)

// Registrar 连接注册管理接口 (连接管理原语)
type Registrar[T any] interface {
	// Register 注册连接; 重复注册返回 ErrAlreadyRegistered
	Register(serviceName string, conn T) error
	// RegisterAndGet 注册连接并立即获取 (等价 Register + Get)
	RegisterAndGet(serviceName string, conn T) (T, error)
	// Unregister 注销连接并执行释放回调 (closer); 未注册返回 ErrNotRegistered
	Unregister(serviceName string) error
	// Get 获取连接; 未注册返回 ErrNotRegistered
	Get(serviceName string) (T, error)
}

// Registry 泛型注册表实现, 线程安全, 按 serviceName 存储连接实例
type Registry[T any] struct {
	mu     sync.RWMutex
	conns  map[string]T
	closer func(serviceName string, conn T) error // 注销时的释放回调 (可为 nil)
}

// NewRegistry 创建注册表; closer 在 Unregister 时调用, 用于释放连接资源
func NewRegistry[T any](closer func(serviceName string, conn T) error) *Registry[T] {
	return &Registry[T]{
		conns:  make(map[string]T),
		closer: closer,
	}
}

// Register 注册连接 (见 Registrar)
func (r *Registry[T]) Register(serviceName string, conn T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.conns[serviceName]; ok {
		return fmt.Errorf("%w: %s", ErrAlreadyRegistered, serviceName)
	}
	r.conns[serviceName] = conn
	return nil
}

// RegisterAndGet 注册连接并立即获取 (见 Registrar)
func (r *Registry[T]) RegisterAndGet(serviceName string, conn T) (T, error) {
	if err := r.Register(serviceName, conn); err != nil {
		var zero T
		return zero, err
	}
	return r.Get(serviceName)
}

// Unregister 注销连接 (见 Registrar)
func (r *Registry[T]) Unregister(serviceName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	conn, ok := r.conns[serviceName]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotRegistered, serviceName)
	}
	delete(r.conns, serviceName)
	if r.closer != nil {
		return r.closer(serviceName, conn)
	}
	return nil
}

// Get 获取连接 (见 Registrar)
func (r *Registry[T]) Get(serviceName string) (T, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.conns[serviceName]
	if !ok {
		var zero T
		return zero, fmt.Errorf("%w: %s", ErrNotRegistered, serviceName)
	}
	return conn, nil
}

// Has 判断连接是否已注册
func (r *Registry[T]) Has(serviceName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.conns[serviceName]
	return ok
}
