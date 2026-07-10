// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package announcement

import (
	"net/http"

	"app/internal/logic/announcement"
	"app/internal/svc"
	"app/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func DeleteAnnouncementHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.DeleteAnnouncementReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := announcement.NewDeleteAnnouncementLogic(r.Context(), svcCtx)
		resp, err := l.DeleteAnnouncement(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
