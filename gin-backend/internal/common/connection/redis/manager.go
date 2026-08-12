package redis

import (
	"fmt"

	"gin-backend/internal/common/connection/registry"
	"gin-backend/internal/config/must"

	goredis "github.com/redis/go-redis/v9"
)

// Manager Redis 连接管理器 (注册模式)
// 业务通过 Register 注册连接封装对象 (注册时收敛初始化, 经 once 检查),
// 经 Get 获取封装对象, 再经 GetConn 获取底层连接; Unregister 注销释放
type Manager struct {
	registry *registry.Registry[*Conn]
}

// RedisManager 全局 Redis 连接管理器
var RedisManager = NewManager()

// NewManager 创建 Redis 连接管理器
func NewManager() *Manager {
	return &Manager{
		registry: registry.NewRegistry(func(_ string, conn *Conn) error {
			return conn.Close()
		}),
	}
}

// Register 注册连接封装对象; 初始化收敛进入注册内 (经 once 检查)
// :Param
// - `serviceName` 业务名, 后续经 Get 按此名获取
// - `cfg` Redis 配置
// :Return
// - `*Conn` 已注册的连接封装对象
// - `error` 如果初始化或注册失败, 返回错误信息
func (m *Manager) Register(serviceName string, cfg *must.RedisConfig) (*Conn, error) {
	conn := newConn(cfg)
	if err := m.registry.Register(serviceName, conn); err != nil {
		return nil, fmt.Errorf("redis: 注册连接 %s 失败: %w", serviceName, err)
	}

	// 初始化收敛进入注册内: 经 once 检查并执行
	if _, err := conn.GetConn(); err != nil {
		_ = m.registry.Unregister(serviceName)
		return nil, err
	}
	return conn, nil
}

// RegisterAndGet 注册连接封装对象并立即获取底层连接 (独立方法, 等价 Register + GetConn)
// :Param
// - `serviceName` 业务名
// - `cfg` Redis 配置
// :Return
// - `*goredis.Client` 已初始化就绪的 Redis 客户端
// - `error` 如果初始化或注册失败, 返回错误信息
func (m *Manager) RegisterAndGet(serviceName string, cfg *must.RedisConfig) (*goredis.Client, error) {
	conn, err := m.Register(serviceName, cfg)
	if err != nil {
		return nil, err
	}
	return conn.GetConn()
}

// Unregister 注销并关闭连接
func (m *Manager) Unregister(serviceName string) error {
	return m.registry.Unregister(serviceName)
}

// Get 获取已注册的连接封装对象
func (m *Manager) Get(serviceName string) (*Conn, error) {
	return m.registry.Get(serviceName)
}
