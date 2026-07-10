package common

import (
	"context"

	"app/internal/types"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// PaginateQuery 执行标准分页查询：先 COUNT 再 SELECT LIMIT/OFFSET。
//
// 参数说明：
//   - T: 数据库行对应的结构体类型
//   - countQuery: COUNT 查询 SQL（不含 LIMIT/OFFSET）
//   - countArgs:  COUNT 查询的参数
//   - dataQuery:  数据查询 SQL（不含 LIMIT/OFFSET，helper 会自动追加）
//   - dataArgs:   数据查询的参数（不含 LIMIT/OFFSET，helper 会自动追加）
//   - pageNumber, pageSize: 分页参数（会自动规范化：pageNumber<1→1，pageSize<1→10）
//
// 返回值：分页信息、数据列表、错误
func PaginateQuery[T any](
	ctx context.Context,
	conn sqlx.SqlConn,
	countQuery string, countArgs []any,
	dataQuery string, dataArgs []any,
	pageNumber, pageSize int,
) (*types.PageInfo, []T, error) {
	// 规范化分页参数
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	// 执行 COUNT 查询
	var total int64
	if err := conn.QueryRowCtx(ctx, &total, countQuery, countArgs...); err != nil {
		return nil, nil, err
	}

	// 追加 LIMIT/OFFSET 并执行数据查询
	limit := int64(pageSize)
	offset := int64(pageNumber-1) * limit
	dataQuery += " LIMIT ? OFFSET ?"
	dataArgs = append(dataArgs, limit, offset)

	var list []T
	if err := conn.QueryRowsCtx(ctx, &list, dataQuery, dataArgs...); err != nil {
		return nil, nil, err
	}

	return &types.PageInfo{
		TotalCount: total,
		PageNumber: pageNumber,
		PageSize:   pageSize,
	}, list, nil
}
