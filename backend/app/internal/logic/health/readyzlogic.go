// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package health

import (
	"context"
	"errors"
	"net/http"

	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ReadyzLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewReadyzLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ReadyzLogic {
	return &ReadyzLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ReadyzLogic) Readyz(client chan<- *types.BaseResp) error {
	if *l.svcCtx.CustomCtx.SqlxConn == nil {
		err := errors.New(MsgDBInitFail)
		l.Logger.Errorf("database not initialized: %v", err)
		client <- &types.BaseResp{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		return nil
	}

	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx, "SELECT 1")
	if err != nil {
		err := errors.New(MsgDBConnFail)
		l.Logger.Errorf("database ping failed: %v", err)
		client <- &types.BaseResp{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		return nil
	}

	if l.svcCtx.CustomCtx.Redis == nil {
		err := errors.New(MsgRedisInitFail)
		l.Logger.Errorf("redis not initialized: %v", err)
		client <- &types.BaseResp{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		return nil
	}

	if !l.svcCtx.CustomCtx.Redis.PingCtx(l.ctx) {
		err := errors.New(MsgRedisConnFail)
		l.Logger.Errorf("redis ping failed: %v", err)
		client <- &types.BaseResp{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		return nil
	}

	if l.svcCtx.CustomCtx.Cache == nil {
		err := errors.New(MsgCacheInitFail)
		l.Logger.Errorf("cache not initialized: %v", err)
		client <- &types.BaseResp{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
		return nil
	}

	client <- &types.BaseResp{
		Code:    http.StatusOK,
		Message: MsgReady,
	}
	return nil

}
