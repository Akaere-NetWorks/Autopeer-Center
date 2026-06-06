package handler

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
)

var registryHandlerLog = logrus.WithField("pkg", "handler.registry")

type RegistryHandler struct {
	registry   *service.RegistryService
	cache      *cache.Cache
	cfg        *config.Config
	rateMu     sync.Mutex
	trustedNets []*net.IPNet
}

func NewRegistryHandler(registry *service.RegistryService, c *cache.Cache, cfg *config.Config, trustedNets []*net.IPNet) *RegistryHandler {
	return &RegistryHandler{registry: registry, cache: c, cfg: cfg, trustedNets: trustedNets}
}

func (h *RegistryHandler) LookupASN(w http.ResponseWriter, r *http.Request) {
	if !h.isAdminRequest(r) {
		ip := middleware.RealIP(r, h.trustedNets)
		if !h.checkIPRate(ip, 10) {
			registryHandlerLog.WithField("ip", ip).Debug("registry lookup IP rate limit hit")
			ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests")
			return
		}
	}

	asnStr := chi.URLParam(r, "asn")
	asn, err := strconv.ParseInt(asnStr, 10, 64)
	if err != nil || asn <= 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid ASN")
		return
	}

	email, err := h.registry.LookupASNEmail(asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "asn_not_found", "ASN not found in DN42 registry")
		return
	}

	result := map[string]interface{}{
		"asn":          asn,
		"masked_email": service.MaskEmail(email),
	}
	if h.isAdminRequest(r) {
		result["email"] = email
	}

	JSON(w, http.StatusOK, result)
}

func (h *RegistryHandler) isAdminRequest(r *http.Request) bool {
	claims, err := middleware.ExtractClaims(h.cfg, r)
	if err != nil {
		return false
	}
	return claims.Role == "admin"
}

func (h *RegistryHandler) checkIPRate(ip string, limit int) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	type ipEntry struct {
		Times []time.Time `json:"times"`
	}
	var entry ipEntry
	h.cache.Get(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, ip), &entry)

	fresh := make([]time.Time, 0, len(entry.Times))
	for _, t := range entry.Times {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}

	if len(fresh) >= limit {
		entry.Times = fresh
		h.cache.Put(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, ip), entry, 2*time.Minute)
		return false
	}

	entry.Times = append(fresh, now)
	h.cache.Put(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, ip), entry, 2*time.Minute)
	return true
}
