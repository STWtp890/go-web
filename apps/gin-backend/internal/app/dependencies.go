package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	basecache "gin-backend/internal/common/base/cache"
	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"
	redisconn "gin-backend/internal/common/base/connection/redis"
	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	"gin-backend/internal/modules/document/infrastructure/documentsearch"
	"gin-backend/internal/modules/document/infrastructure/documentservice"
	sourceowned "gin-backend/internal/modules/document/interfaces/sourceowned"
	"gin-backend/internal/platform/httpserver"

	"packages/serviceauth"
)

type runtimeDependencies struct {
	documentHTTP httpserver.RouteRegistrar
	// documentServiceClient and documentSearchClient are the ADR-017 boundaries.
	// They are nil when the source-owned services are not configured, and the
	// Web document routes are then absent rather than backed by direct table
	// access.
	documentServiceClient *documentservice.Client
	documentSearchClient  *documentsearch.Client
}

// ready 校验当前已接入的数据库与 Redis 基础设施均可用。
//
// 这里刻意只检查 gin-backend 自己拥有的连接：ADR-017 之后 Web 文档面经
// document-service 与 document-search 访问正式文档，本进程不再注册、也不再
// 健康检查文档业务库连接。
func (dependencies *runtimeDependencies) ready(ctx context.Context) error {
	pg, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	db, err := pg.GetConn()
	if err != nil {
		return err
	}
	if err := postgresqlconn.HealthCheck(db); err != nil {
		return err
	}

	rc, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	client, err := rc.GetConn()
	if err != nil {
		return err
	}
	return client.Ping(ctx).Err()
}

// initDependencies 初始化全局基础设施和业务模块，并返回依赖集合与统一清理函数。
func initDependencies(conf *config.Config) (*runtimeDependencies, func()) {
	if _, err := postgresqlconn.PostgreSQLManager.RegisterAndGet(connection.ServiceAuth, conf.PostgresConfig, conf.LogConfig.Level); err != nil {
		panic(fmt.Sprintf("数据库连接失败: %v", err))
	}

	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceAuth, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	authRedisConn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("获取认证 Redis 连接失败: %v", err))
	}
	authRedisClient, err := authRedisConn.GetConn()
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("获取认证 Redis 客户端失败: %v", err))
	}
	sessionBus, err := sessionevent.NewRedisPubSubBus(authRedisClient)
	if err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("初始化会话事件总线失败: %v", err))
	}
	sessionevent.SetDefaultBus(sessionBus)

	if _, err := redisconn.RedisManager.RegisterAndGet(connection.ServiceCache, &conf.RedisConfig); err != nil {
		_ = postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth)
		_ = redisconn.RedisManager.Unregister(connection.ServiceAuth)
		panic(fmt.Sprintf("Redis 连接失败: %v", err))
	}

	// ADR-017：正式文档与空间的唯一写入方是 document-service。启用来源专属服务
	// 时，Web 文档路由整体切到服务接口（写命令 + 详情读取 + 经 capability 的检索），
	// 不再读写文档业务表。未启用时不注册文档路由，而不是退回直接写表。
	var documentRouteHandler httpserver.RouteRegistrar
	var documentServiceClient *documentservice.Client
	var documentSearchClient *documentsearch.Client
	if conf.SourceOwnedServices.Enabled {
		// The boundary key loader lives in the shared boundary package: go-web no
		// longer has a mixin-search client to own it.
		boundaryKey, err := serviceauth.LoadBoundaryKeyFile(conf.SourceOwnedServices.CapabilityKeyPath)
		if err != nil {
			panic(fmt.Sprintf("读取来源专属服务边界密钥失败: %v", err))
		}
		documentServiceClient, err = documentservice.New(documentservice.Config{
			Endpoint:       conf.SourceOwnedServices.DocumentServiceAddress,
			Audience:       conf.SourceOwnedServices.DocumentServiceAudience,
			Caller:         serviceauth.Caller(conf.SourceOwnedServices.Caller),
			Actor:          conf.SourceOwnedServices.Actor,
			Scopes:         []serviceauth.Scope{serviceauth.ScopeDocumentWrite, serviceauth.ScopeDocumentRead},
			RequestTimeout: conf.SourceOwnedServices.RequestTimeout,
		}, boundaryKey)
		if err != nil {
			panic(fmt.Sprintf("初始化文档服务客户端失败: %v", err))
		}
		documentSearchClient, err = documentsearch.New(documentsearch.Config{
			Endpoint:       conf.SourceOwnedServices.DocumentSearchAddress,
			Audience:       conf.SourceOwnedServices.DocumentSearchAudience,
			Caller:         serviceauth.Caller(conf.SourceOwnedServices.Caller),
			Actor:          conf.SourceOwnedServices.Actor,
			RequestTimeout: conf.SourceOwnedServices.RequestTimeout,
		}, boundaryKey)
		if err != nil {
			panic(fmt.Sprintf("初始化文档检索客户端失败: %v", err))
		}
		gateway, err := sourceowned.New(sourceowned.Config{
			Documents: documentServiceClient,
			Search:    documentSearchClient,
		})
		if err != nil {
			panic(fmt.Sprintf("初始化文档服务网关失败: %v", err))
		}
		serviceHandler, err := sourceowned.NewServiceAdapter(gateway)
		if err != nil {
			panic(fmt.Sprintf("初始化文档服务 HTTP 适配器失败: %v", err))
		}
		documentRouteHandler = serviceHandler
	}

	// 数据库结构由 PostgreSQL 空卷初始化脚本建立，不在服务启动时迁移。
	// Chat 包使用独立 ServiceChat 名称，但当前不注册连接或路由。
	dependencies := &runtimeDependencies{
		documentHTTP:          documentRouteHandler,
		documentServiceClient: documentServiceClient,
		documentSearchClient:  documentSearchClient,
	}
	stopCacheStats := basecache.DefaultRuntime().StartStatsLogger(time.Minute)
	return dependencies, func() {
		stopCacheStats()
		if dependencies.documentServiceClient != nil {
			if err := dependencies.documentServiceClient.Close(); err != nil {
				slog.Error("关闭文档服务连接失败", slog.String("error", err.Error()))
			}
		}
		if dependencies.documentSearchClient != nil {
			if err := dependencies.documentSearchClient.Close(); err != nil {
				slog.Error("关闭文档检索服务连接失败", slog.String("error", err.Error()))
			}
		}
		sessionevent.SetDefaultBus(nil)
		if err := postgresqlconn.PostgreSQLManager.Unregister(connection.ServiceAuth); err != nil {
			slog.Error("关闭数据库连接失败", slog.String("error", err.Error()))
		}
		if err := redisconn.RedisManager.Unregister(connection.ServiceAuth); err != nil {
			slog.Error("关闭 Redis 连接失败", slog.String("error", err.Error()))
		}
		if err := redisconn.RedisManager.Unregister(connection.ServiceCache); err != nil {
			slog.Error("关闭缓存 Redis 连接失败", slog.String("error", err.Error()))
		}
	}
}
