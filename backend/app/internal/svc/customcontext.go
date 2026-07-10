package svc

import (
	"app/internal/config"
	anno "app/internal/model/announcement"
	"app/internal/model/auth"
	md "app/internal/model/markdown"
	"app/internal/model/rbac"
	"errors"

	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/core/syncx"
)

type CustomContext struct {
	// custom:
	Redis    *redis.Redis
	Cache    cache.Cache
	SqlxConn *sqlx.SqlConn
	SQLModel *SQLModel
}

func NewCustomContext(c config.Config) CustomContext {

	redisClient, err := redis.NewRedis(c.RedisConf)
	if err != nil {
		panic(err)
	}

	// cacheClient, err := cache.NewNode(redisClient, barrier, )
	cacheClient := cache.New(
		c.CacheConf,
		syncx.NewSingleFlight(),
		cache.NewStat(c.Name+"cache"),
		errors.New("cache miss"),
	)

	sqlxConn := sqlx.NewSqlConn(
		c.SqlxConf.DriverName,
		c.SqlxConf.DataSource,
	)

	sqlModel := NewSQLModel(sqlxConn, c.CacheConf)

	return CustomContext{
		Redis:    redisClient,
		Cache:    cacheClient,
		SqlxConn: &sqlxConn,
		SQLModel: sqlModel,
	}
}

// SQLModel 封装了 goctl 生成的 model，方便在 service 中使用
type SQLModel struct {
	// auth service
	auth.UsersModel
	// rbac service
	rbac.UserRolesModel
	rbac.RolesModel
	rbac.RolePermissionsModel
	rbac.PermissionsModel
	// announcement service
	anno.AnnouncementModel
	anno.AnnouncementContentModel
	// markdown service
	md.MarkdownModel
	md.MarkdownContentModel
	md.MarkdownReviewInfoModel
}

// NewSQLModel 返回 SQLModel 实例
func NewSQLModel(conn sqlx.SqlConn, cacheConf cache.CacheConf) *SQLModel {
	return &SQLModel{
		UsersModel:               auth.NewUsersModel(conn, cacheConf),
		UserRolesModel:           rbac.NewUserRolesModel(conn, cacheConf),
		RolesModel:               rbac.NewRolesModel(conn, cacheConf),
		RolePermissionsModel:     rbac.NewRolePermissionsModel(conn, cacheConf),
		PermissionsModel:         rbac.NewPermissionsModel(conn, cacheConf),
		AnnouncementModel:        anno.NewAnnouncementModel(conn, cacheConf),
		AnnouncementContentModel: anno.NewAnnouncementContentModel(conn, cacheConf),
		MarkdownModel:            md.NewMarkdownModel(conn, cacheConf),
		MarkdownContentModel:     md.NewMarkdownContentModel(conn, cacheConf),
		MarkdownReviewInfoModel:  md.NewMarkdownReviewInfoModel(conn, cacheConf),
	}
}
