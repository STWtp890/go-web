// Package postgresql 提供 PostgreSQL(GORM) 连接的创建与初始化 (纯工厂, 无全局状态),
// 连接生命周期由业务通过 PostgreSQLManager 注册 / 注销维护。
package postgresql

import (
	"fmt"
	"log/slog"
	"time"

	"gin-backend/internal/config/must"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewDB 创建并初始化 GORM PostgreSQL 连接 (DSN + 连接池 + Ping 验证)
// logLevel: 应用日志级别, debug 时 SQL 日志输出 Info 级别
// :Return
// - `*gorm.DB` 已就绪的数据库连接
// - `error` 如果连接失败, 返回错误信息
func NewDB(cfg must.PostgresConfig, logLevel string) (*gorm.DB, error) {
	dsn := buildDSN(cfg)

	db, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{
		Logger: newSlogLogger(logLevel),
		// 禁用默认事务——单次 Create/Update 不需要事务包装
		SkipDefaultTransaction: true,
		// 预编译语句缓存
		PrepareStmt: true,
	})
	if err != nil {
		return nil, fmt.Errorf("gorm: 连接 PostgreSQL 失败: %w", err)
	}

	// 配置底层连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("gorm: 获取 sql.DB 失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	// 启动时 Ping 验证
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("gorm: PostgreSQL Ping 失败: %w", err)
	}

	slog.Info("gorm: PostgreSQL 连接成功",
		slog.String("host", cfg.Host),
		slog.Int("port", cfg.Port),
		slog.String("db", cfg.DBName),
		slog.Int("max_open", cfg.MaxOpenConns),
		slog.Int("max_idle", cfg.MaxIdleConns),
	)

	return db, nil
}

// Close 关闭数据库连接
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	slog.Info("gorm: 正在关闭 PostgreSQL 连接...")
	return sqlDB.Close()
}

// HealthCheck 数据库健康检查
func HealthCheck(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("gorm: 数据库未初始化")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	start := time.Now()
	if err := sqlDB.Ping(); err != nil {
		return err
	}

	slog.Debug("gorm: PostgreSQL 健康检查通过",
		slog.Duration("latency", time.Since(start)),
		slog.Int("open_conns", sqlDB.Stats().OpenConnections),
		slog.Int("idle_conns", sqlDB.Stats().Idle),
	)

	return nil
}

// Transaction 事务函数装饰器
func Transaction(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Transaction(fn)
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB, models ...any) error {
	if err := db.AutoMigrate(models...); err != nil {
		slog.Error("gorm: PostgreSQL 自动迁移失败", slog.Any("error", err))
		return err
	}
	slog.Info("gorm: PostgreSQL 自动迁移完成")
	return nil
}

// buildDSN 构建 PostgreSQL DSN (key=value 格式)
// sslmode 默认关闭 (本地/内网部署场景)
func buildDSN(cfg must.PostgresConfig) string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%d sslmode=disable TimeZone=Asia/Shanghai",
		cfg.Host,
		cfg.User,
		cfg.Password,
		cfg.DBName,
		cfg.Port,
	)
}
