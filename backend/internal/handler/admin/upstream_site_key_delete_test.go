package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeleteUpstreamSiteKeyRequiresConfirmation(t *testing.T) {
	for _, body := range []string{"", "{}", `{"revision":1,"confirmed":false}`, `{"confirmed":"yes"}`} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodDelete, "/keys/7", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		// Rejection must happen before any service or upstream access.
		(&AccountHandler{}).DeleteUpstreamSiteKey(ctx)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
}
