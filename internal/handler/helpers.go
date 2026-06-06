package handler

import (
	"encoding/json"
	"net/http"

	"github.com/akaere/autopeer-center/internal/apiversion"
	"github.com/akaere/autopeer-center/internal/middleware"
)

func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// JSONVersioned writes data as the latest canonical shape, downgraded to the
// request's resolved Autopeer-Version (see internal/apiversion). Handlers build
// only the latest shape and call this for versioned resources.
//
// res is the resource kind being serialized; listKey names the field that holds
// the resource objects when data is a wrapper map (e.g. "peers" for
// {"peers":[...], "total":N}) — pass "" for a bare object or a bare list. When
// the request targets the latest version this is a zero-overhead fast path
// identical to JSON.
func JSONVersioned(w http.ResponseWriter, r *http.Request, status int, res apiversion.Resource, listKey string, data any) {
	target := middleware.GetAPIVersion(r.Context())
	if target == "" {
		target = apiversion.Latest()
	}
	if target == apiversion.Latest() {
		JSON(w, status, data)
		return
	}

	raw, err := json.Marshal(data)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to encode response")
		return
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to encode response")
		return
	}
	JSON(w, status, apiversion.Downgrade(res, target, generic, listKey))
}

func ErrorJSON(w http.ResponseWriter, r *http.Request, status int, errCode, message string) {
	reqID := middleware.GetRequestID(r.Context())
	JSON(w, status, map[string]interface{}{
		"error":      errCode,
		"message":    message,
		"request_id": reqID,
	})
}

func NoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func DecodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

type peerUpdateBody struct {
	RemotePubkey   *string `json:"remote_pubkey"`
	RemoteEndpoint *string `json:"remote_endpoint"`
	RemoteLLA      *string `json:"remote_lla"`
	MTU            *int    `json:"mtu"`
	MTUSet         bool    `json:"-"`
}

func decodePeerUpdateJSON(r *http.Request) (*peerUpdateBody, error) {
	defer r.Body.Close()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, err
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}

	var body peerUpdateBody
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	_, body.MTUSet = raw["mtu"]

	return &body, nil
}
