// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package health

import (
	"context"
	"net/http"

	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type HealthzLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewHealthzLogic(ctx context.Context, svcCtx *svc.ServiceContext) *HealthzLogic {
	return &HealthzLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *HealthzLogic) Healthz(client chan<- *types.BaseResp) error {
	if l.ctx.Err() != nil {
		l.Logger.Errorf("Server context is canceled or timed out")
		client <- &types.BaseResp{
			Code: http.StatusInternalServerError,
			Message:  MsgUnhealthy,
		}
	} else {
		client <- &types.BaseResp{
			Code: http.StatusOK,
			Message:  MsgHealthy,
		}
	}
	
	return nil
}
