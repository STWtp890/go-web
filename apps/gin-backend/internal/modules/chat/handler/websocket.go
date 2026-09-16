package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/modules/chat/logic"
	chatclient "gin-backend/internal/modules/chat/types/client"
	"gin-backend/internal/platform/httpserver/identity"

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
	// 身份由 AuthRequired 中间件解析并注入, 此处只读取结果
	principal, exists := identity.FromGin(c)
	if !exists || principal.Subject == "" {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	if principal.SessionID == "" {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
		return
	}

	// 协议升级 HTTP → WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 * 1024)

	// Handler 负责将具体协议连接适配为聊天模块使用的 Connection 接口。
	transport := chatclient.NewWebSocketClient(c.Request.Context(), conn)
	logic.WebSocketLogic(c.Request.Context(), principal.Subject, principal.SessionID, transport)
}
