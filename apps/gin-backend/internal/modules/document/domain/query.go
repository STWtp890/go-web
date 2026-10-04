package domain

import (
	"time"
)

// Page 是列表与检索共用的分页信封。
//
// 它放在领域层，因为它同时是 application、infrastructure 与 interfaces 的返回
// 形状；放在其中任一层都会让另外两层为了一个纯数据结构而反向依赖。
//
// NextCursor 是来源服务给出的不透明前向游标，调用方只能原样回传。Number 只用于
// 展示：游标分页下服务端不再按页码换算偏移，页码由前端根据游标历史自行维护。
//
// Truncated 是检索服务给出的“结果未完整展示”信号：本页之后仍有结果。列表分页没有
// 这个概念，保持 false。
type Page struct {
	Number     int
	Size       int
	Total      int64
	NextCursor string
	Truncated  bool
}

// DocumentHead 是构造版本化缓存键和执行访问判断所需的最小当前状态。
type DocumentHead struct {
	DocumentID          string
	OwnerID             int64
	ActiveVersionID     string
	AuthenticatedPublic bool
	ActivationRevision  int64
	AccessRevision      int64
	LifecycleRevision   int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// DocumentView 是当前活动版本的详情读模型。
type DocumentView struct {
	DocumentHead
	Title   string
	Summary string
	Content string
}

// DocumentSummary 是列表和搜索使用的当前版本投影。
type DocumentSummary struct {
	DocumentID          string
	OwnerID             int64
	AuthenticatedPublic bool
	Title               string
	Summary             string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
