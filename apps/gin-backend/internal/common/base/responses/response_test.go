package responses

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOKWithMetaKeepsZeroPaginationValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/", func(c *gin.Context) {
		OKWithMeta(c, gin.H{"items": []string{}}, &Meta{
			Page:       1,
			PerPage:    10,
			Total:      0,
			TotalPages: 0,
		})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", recorder.Code, http.StatusOK)
	}
	var body struct {
		Meta map[string]int `json:"meta"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, field := range []string{"page", "per_page", "total", "total_pages"} {
		if _, ok := body.Meta[field]; !ok {
			t.Fatalf("pagination field %q is missing from zero-result response", field)
		}
	}
	if body.Meta["total"] != 0 || body.Meta["total_pages"] != 0 {
		t.Fatalf("unexpected zero-result meta: %#v", body.Meta)
	}
}
