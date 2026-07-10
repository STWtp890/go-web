package health

// 响应消息常量
const (
	MsgHealthy   = "Server is healthy."
	MsgUnhealthy = "Server is unhealthy!"
	MsgReady     = "Server is ready."
	MsgDBInitFail    = "Server is not initialized with database initialized failed"
	MsgDBConnFail    = "Server is not initialized with database connection failed"
	MsgRedisInitFail = "Server is not initialized with redis initialized failed"
	MsgRedisConnFail = "Server is not initialized with redis connection failed"
	MsgCacheInitFail = "Server is not initialized with cache initialized failed"
)
