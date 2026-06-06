package handler

import (
	"net/http"

	"github.com/akaere/autopeer-center/internal/middleware"
)

func AssistantAuthCheck(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"role":       middleware.GetRole(r.Context()),
		"asn":        middleware.GetASN(r.Context()),
		"session_id": middleware.GetSessionID(r.Context()),
	})
}
