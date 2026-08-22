package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/chat/logic"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return browserOriginAllowed(r)
	},
}

// WebSocketHandler WebSocket 入口
func WebSocketHandler(c *gin.Context) {
	// 参数检验 & 参数提取
	claims, exists := jwt.ExtractClaims(c)
	if !exists {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	sub, err := claims.GetSubject()
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	sessionID, ok := jwt.SessionIDFromClaims(*claims)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
		return
	}

	// 协议升级 HTTP → WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 * 1024)

	// 传递实际 WebSocket Connection 至 Logic 层 (阻塞直至连接关闭)
	logic.WebSocketLogic(c.Request.Context(), sub, sessionID, conn)
}
