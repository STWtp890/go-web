// server 是 gin-backend HTTP 服务的可执行入口。
package main

import (
	"fmt"
	"os"

	"gin-backend/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gin-backend 退出: %v\n", err)
		os.Exit(1)
	}
}
