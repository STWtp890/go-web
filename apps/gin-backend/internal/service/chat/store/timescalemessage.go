// 聊天消息持久化 (TimescaleDB): TimescaleMessageStore 实现
//
// TimescaleDB 是 PostgreSQL 扩展, 兼容 PG 协议 — 复用 gormStore 基座与
// gorm.io/driver/postgres 驱动, 无需新增驱动或连接。
//
// 与 GormMessageStore 的差异 (hypertable 适配):
//   - DeleteByIDs 用 Where("id IN ?") 而非 Delete(&Message{}, ids):
//     hypertable 主键为复合键 (created_at, id), GORM 按单主键 slice 删除不适用;
//     该写法对单主键/复合主键均成立。
//   - 读路径显式按时间列 (created_at, id) 排序, 不依赖自增 id 单调性假设
//     (跨时钟漂移 / 批量导入时 id 与时间列可能不一致, 见 chat-timescaledb.md §6)。
//   - 查询自动受益于 hypertable 分区裁剪与覆盖索引
//     idx_message_to_type_time(to_id, group_type, created_at DESC, id DESC)。
//
// 前置: chat_messages 已由部署脚本 deployments/postgresql/sql/service/chat/timescaledb_setup.sql
// 转为 hypertable (IsHypertable 可运行时诊断); 未转换时本实现退化为普通表语义, 查询仍正确。
package store

import (
	"context"

	ormchat "gin-backend/internal/model/orm/chat"
	"gin-backend/internal/service/chat/types/constant"
	"gin-backend/internal/service/chat/types/message"
)

// TimescaleMessageStore MessageStore 的 TimescaleDB 实现 (hypertable 就绪)
// 复用 gormStore 基座: 懒加载复用 ServiceMarkdown 的 PostgreSQL 连接
type TimescaleMessageStore struct {
	gormStore
}

// NewTimescaleMessageStore 创建 TimescaleDB 消息存储 (无参, DB 连接懒加载)
func NewTimescaleMessageStore() *TimescaleMessageStore {
	return &TimescaleMessageStore{}
}

// Save 落库一条消息 (Payload 为 wire 格式 JSON, 读取时经 message.Unmarshal 还原)
// 经 TimeScaleMessage 纯类型构造字段, 再映射 orm 模型落库 — 保留 GORM 的
// 自增主键 / created_at autoCreateTime / deleted_at 软删特性 (hypertable 写入落在当前 chunk)
func (s *TimescaleMessageStore) Save(ctx context.Context, m message.Message) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	ts := message.NewTimeScaleMessage(m)
	row := ormchat.Message{
		GroupType: ts.GroupType,
		FromID:    ts.FromID,
		ToID:      ts.ToID,
		Payload:   string(ts.Payload),
	}
	return db.WithContext(ctx).Create(&row).Error
}

// FetchOffline 拉取私聊离线未读消息 (to_id = 接收者), 按 (created_at, id) 升序保证时序
// 直接以纯类型 TimeScaleMessage 作为 GORM 扫描目标 (字段名与表列一一对应, Table 显式指定)
// 软删过滤手动附加 deleted_at IS NULL (纯类型无 gorm.DeletedAt, 无自动软删)
// 显式按时间列排序: 分区裁剪后命中 chunk 内走覆盖索引, 且不依赖 id 单调假设
func (s *TimescaleMessageStore) FetchOffline(ctx context.Context, toID string, limit int) ([]message.TimeScaleMessage, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []message.TimeScaleMessage
	if err := db.WithContext(ctx).
		Table("chat_messages").
		Where("to_id = ? AND group_type = ? AND deleted_at IS NULL", toID, constant.GroupPrivate).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// FetchGroupHistory 拉取群聊历史 (to_id = 群 ID): 最新 limit 条, 反转回时序正序
// 直接以纯类型 TimeScaleMessage 作为 GORM 扫描目标; 软删过滤手动附加 deleted_at IS NULL
// 按 (created_at, id) 倒序取最新, 与覆盖索引 idx_message_to_type_time 的
// (to_id, group_type, created_at DESC, id DESC) 排序方向一致, chunk 内索引扫描即完成排序
func (s *TimescaleMessageStore) FetchGroupHistory(ctx context.Context, groupID string, limit int) ([]message.TimeScaleMessage, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []message.TimeScaleMessage
	if err := db.WithContext(ctx).
		Table("chat_messages").
		Where("to_id = ? AND group_type = ? AND deleted_at IS NULL", groupID, constant.GroupGroup).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

// DeleteByIDs 标记已投递消息为软删除 (保留历史可审计)
// hypertable 主键为复合键 (created_at, id), GORM 的 Delete(&Message{}, ids) 按单主键
// slice 删除在复合主键下不适用 → 显式 Where("id IN ?"), 对单主键/复合主键均兼容
// 物理清理由 sidecar 定时任务批量执行 (硬删不在此路径, 避免阻塞投递)
func (s *TimescaleMessageStore) DeleteByIDs(ctx context.Context, ids []uint) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return db.WithContext(ctx).
		Where("id IN ?", ids).
		Delete(&ormchat.Message{}).Error
}

// IsHypertable 运行时诊断: 检查 chat_messages 是否已转换为 TimescaleDB hypertable
// 供运维/测试确认部署就绪; 返回 false 表示仍为普通表 (本实现退化为普通表语义, 查询仍正确)
// 注意: timescaledb 扩展未安装时 timescaledb_information 视图不存在, 查询会返回 error
func (s *TimescaleMessageStore) IsHypertable(ctx context.Context) (bool, error) {
	db, err := s.dbConn()
	if err != nil {
		return false, err
	}
	var n int64
	if err := db.WithContext(ctx).
		Raw(`SELECT count(*) FROM timescaledb_information.hypertables WHERE hypertable_name = 'chat_messages'`).
		Scan(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}
