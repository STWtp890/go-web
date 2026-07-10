// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package svc

import (
	"app/internal/config"
)

type ServiceContext struct {
	Config    config.Config
	CustomCtx CustomContext
}

func NewServiceContext(c config.Config) *ServiceContext {
	return &ServiceContext{
		Config:    c,
		CustomCtx: NewCustomContext(c),
	}
}
