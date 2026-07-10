// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"

	"app/internal/common"
	"app/internal/config"
	"app/internal/handler"
	"app/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"
)

var configFile = flag.String("f", "etc/api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 全局错误处理器：BizError → 正确 HTTP 状态码 + JSON body
	httpx.SetErrorHandlerCtx(func(ctx context.Context, err error) (int, any) {
		var bizErr *common.BizError
		if errors.As(err, &bizErr) {
			return bizErr.HTTPStatus, bizErr
		}
		return http.StatusInternalServerError, map[string]any{
			"code":    500,
			"message": err.Error(),
		}
	})

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	fmt.Printf("Remember the API Prefix with '/api/v1'\n")
	server.Start()
}
