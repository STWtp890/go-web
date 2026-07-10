package common

import (
	"math/rand"
	"time"
)

// Now 返回当前 UTC 毫秒精度时间，作为项目中统一的时间获取入口。
// 精度匹配数据库 DATETIME(3) 类型。
// 将来如需替换为模拟时钟（测试），只需修改此处。
func Now() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

func ExpireTimeWithTtl(baseTime time.Duration, ttl time.Duration) time.Duration {
	return baseTime + time.Duration(rand.Int63n(int64(ttl)))
}