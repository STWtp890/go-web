// TimescaleDB 消息类型: 消息在 hypertable 中的持久化形态与时间桶聚合类型
//
// TimescaleDB 是 PostgreSQL 扩展, chat_messages 转为 hypertable 后:
//   - 分区时间列: CreatedAt (Unix 秒, 整数时间), 与自增 ID 组成复合主键 (created_at, id)
//   - Payload: wire 格式 JSON (Message.Marshal() 产物), 读取时经 Unmarshal 还原
//
// 本文件为纯类型层 (不依赖 GORM/驱动), 供 store 层 (TimescaleMessageStore)
// 与未来时间桶聚合 (time_bucket / 连续聚合) 复用。
package message

// TimeScaleMessage 消息的 TimescaleDB 持久化形态 (hypertable 行 ↔ Message 互转)
// :Field
// - ID: 自增业务 id (与 CreatedAt 组成 hypertable 复合主键)
// - GroupType/FromID/ToID: 与 Message 一一对应
// - Payload: wire 格式 JSON 字节
// - CreatedAt: Unix 秒 (hypertable 分区时间列), 由落库方 (store) 设置为入库时间
type TimeScaleMessage struct {
	ID        uint   `json:"id"`
	GroupType string `json:"group_type"`
	FromID    string `json:"from_id"`
	ToID      string `json:"to_id"`
	Payload   []byte `json:"payload"`
	CreatedAt int64  `json:"created_at"`
}

// NewTimeScaleMessage 从 Message 构造持久化形态
// CreatedAt 不在此处设置: 入库时间应由落库方决定 (避免信任客户端时间戳, 防伪造)
func NewTimeScaleMessage(m Message) *TimeScaleMessage {
	return &TimeScaleMessage{
		GroupType: m.GroupType(),
		FromID:    m.From(),
		ToID:      m.To(),
		Payload:   m.Marshal(),
	}
}

// ToMessage 还原为传输层 Message (wire 格式反序列化)
func (t *TimeScaleMessage) ToMessage() (Message, error) {
	return Unmarshal(t.Payload)
}

// TimeBucketCount 时间桶聚合结果 (TimescaleDB time_bucket / 连续聚合)
// 用于消息量/活跃度等时间维度分析 (见 deployments/postgresql/chat-timescaledb.md 阶段4)
// :Field
// - Bucket: 桶起始时间 (Unix 秒, 整数时间桶)
// - ToID: 聚合维度 (私聊接收者 / 群 ID)
// - GroupType: private | group
// - Count: 桶内消息数
type TimeBucketCount struct {
	Bucket    int64  `json:"bucket"`
	ToID      string `json:"to_id"`
	GroupType string `json:"group_type"`
	Count     int64  `json:"count"`
}
