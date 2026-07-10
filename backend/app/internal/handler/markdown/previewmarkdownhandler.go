// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package markdown

import (
	"net/http"

	"app/internal/logic/markdown"
	"app/internal/svc"
	"app/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func PreviewMarkdownHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.PreviewMarkdownReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := markdown.NewPreviewMarkdownLogic(r.Context(), svcCtx)
		resp, err := l.PreviewMarkdown(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
