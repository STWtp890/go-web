package postgresql

import (
	"context"
	"log/slog"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

// ---------- slog 适配 GORM Logger 接口 ----------

// slogLogger 实现 gormlogger.Interface, 将 GORM 日志桥接到 slog
type slogLogger struct {
	SlowThreshold time.Duration
	LogLevel      gormlogger.LogLevel
}

func newSlogLogger(gormLogLevel string) gormlogger.Interface {
	level := gormlogger.Warn // 默认：Warn 以上才输出慢查询
	if gormLogLevel == "debug" {
		level = gormlogger.Info // debug 模式输出所有 SQL
	}

	return &slogLogger{
		SlowThreshold: 200 * time.Millisecond, // 慢查询阈值
		LogLevel:      level,
	}
}

func (l *slogLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

func (l *slogLogger) Info(_ context.Context, msg string, args ...any) {
	if l.LogLevel >= gormlogger.Info {
		slog.Info("gorm", slog.String("msg", msg), slog.Any("args", args))
	}
}

func (l *slogLogger) Warn(_ context.Context, msg string, args ...any) {
	if l.LogLevel >= gormlogger.Warn {
		slog.Warn("gorm", slog.String("msg", msg), slog.Any("args", args))
	}
}

func (l *slogLogger) Error(_ context.Context, msg string, args ...any) {
	if l.LogLevel >= gormlogger.Error {
		slog.Error("gorm", slog.String("msg", msg), slog.Any("args", args))
	}
}

// Trace 记录 SQL 执行日志（包含耗时、行数）
func (l *slogLogger) Trace(_ context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	switch {
	case err != nil && l.LogLevel >= gormlogger.Error:
		slog.Error("gorm:sql",
			slog.Duration("elapsed", elapsed),
			slog.Int64("rows", rows),
			slog.String("sql", sql),
			slog.Any("error", err),
		)
	case elapsed > l.SlowThreshold && l.LogLevel >= gormlogger.Warn:
		slog.Warn("gorm:slow_query",
			slog.Duration("elapsed", elapsed),
			slog.Int64("rows", rows),
			slog.String("sql", sql),
		)
	case l.LogLevel >= gormlogger.Info:
		slog.Debug("gorm:sql",
			slog.Duration("elapsed", elapsed),
			slog.Int64("rows", rows),
			slog.String("sql", sql),
		)
	}
}
