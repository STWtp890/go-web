package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwtmethod"
	logic "gin-backend/internal/service/chat/logic"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// WebSocketHandler WebSocket 入口
func WebSocketHandler(c *gin.Context) {
	// 参数检验 & 参数提取
	claims, exists := jwtmethod.ExtractClaims(c)
	if !exists {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	sub, err := claims.GetSubject()
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	// 协议升级 HTTP → WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil) // "If the upgrade fails, then Upgrade replies to the client with an HTTP error response."
	if err != nil {                                         // 升级失败, 则 Upgrader 自动回复客户端 HTTP 错误响应, 直接返回
		responses.Fail(c, http.StatusBadRequest, eror.CodeUpgradeError, "WebSocket 握手失败")
		return
	}

	// 传递实际 WebSocket Connection 至 Logic 层 (阻塞直至连接关闭)
	logic.WebSocketLogic(c.Request.Context(), sub, conn)
}
