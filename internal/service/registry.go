package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/sirupsen/logrus"
)

var registryLog = logrus.WithField("pkg", "service.registry")

type RegistryService struct {
	token      string
	httpClient *http.Client
	cache      *cache.Cache
}

func NewRegistryService(token string, c *cache.Cache) *RegistryService {
	return &RegistryService{
		token: token,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		cache: c,
	}
}

func (s *RegistryService) LookupASNEmail(asn int64) (string, error) {
	registryLog.WithField("asn", asn).Debug("LookupASNEmail entry")

	var email string
	if ok, _ := s.cache.Get(cache.BucketRegistry, cache.ASNKey(cache.RegistryASN, asn), &email); ok {
		registryLog.WithField("asn", asn).Debug("cache hit")
		return email, nil
	}
	registryLog.WithField("asn", asn).Debug("cache miss")

	autNumURL := fmt.Sprintf("https://git.dn42.dev/dn42/registry/raw/branch/master/data/aut-num/AS%d", asn)
	registryLog.WithField("url", autNumURL).Debug("fetching aut-num object")

	autNumData, err := s.fetchURL(autNumURL)
	if err != nil {
		registryLog.WithError(err).WithField("asn", asn).Error("fetch aut-num failed")
		return "", fmt.Errorf("fetch aut-num: %w", err)
	}

	adminC := parseField(autNumData, "admin-c")
	registryLog.WithFields(logrus.Fields{
		"field": "admin-c",
		"value": adminC,
	}).Debug("parsed field from aut-num")
	if adminC == "" {
		registryLog.WithField("asn", asn).Warn("admin-c not found in aut-num object")
		return "", fmt.Errorf("admin-c not found for AS%d", asn)
	}

	personURL := fmt.Sprintf("https://git.dn42.dev/dn42/registry/raw/branch/master/data/person/%s", adminC)
	registryLog.WithField("url", personURL).Debug("fetching person object")

	personData, err := s.fetchURL(personURL)
	if err != nil {
		registryLog.WithError(err).WithField("person", adminC).Error("fetch person failed")
		return "", fmt.Errorf("fetch person %s: %w", adminC, err)
	}

	email = parseField(personData, "e-mail")
	registryLog.WithFields(logrus.Fields{
		"field": "e-mail",
		"value": MaskEmail(email),
	}).Debug("parsed field from person")
	if email == "" {
		registryLog.WithField("person", adminC).Warn("e-mail not found in person object")
		return "", fmt.Errorf("e-mail not found for person %s", adminC)
	}

	s.cache.Put(cache.BucketRegistry, cache.ASNKey(cache.RegistryASN, asn), email, 5*time.Minute)
	registryLog.WithField("asn", asn).Debug("result cached")

	registryLog.WithFields(logrus.Fields{
		"asn":   asn,
		"email": MaskEmail(email),
	}).Debug("LookupASNEmail result")

	return email, nil
}

func (s *RegistryService) LookupMntnerGPGFingerprints(asn int64) ([]string, error) {
	registryLog.WithField("asn", asn).Debug("LookupMntnerGPGFingerprints entry")

	var fingerprints []string
	cacheKey := cache.ASNKey(cache.RegistryGPG, asn)
	if ok, _ := s.cache.Get(cache.BucketRegistry, cacheKey, &fingerprints); ok {
		registryLog.WithField("asn", asn).Debug("GPG fingerprints cache hit")
		return fingerprints, nil
	}
	registryLog.WithField("asn", asn).Debug("GPG fingerprints cache miss")

	autNumURL := fmt.Sprintf("https://git.dn42.dev/dn42/registry/raw/branch/master/data/aut-num/AS%d", asn)
	autNumData, err := s.fetchURL(autNumURL)
	if err != nil {
		return nil, fmt.Errorf("fetch aut-num: %w", err)
	}

	mntBy := parseField(autNumData, "mnt-by")
	if mntBy == "" {
		return nil, fmt.Errorf("mnt-by not found for AS%d", asn)
	}

	mntnerURL := fmt.Sprintf("https://git.dn42.dev/dn42/registry/raw/branch/master/data/mntner/%s", mntBy)
	mntnerData, err := s.fetchURL(mntnerURL)
	if err != nil {
		return nil, fmt.Errorf("fetch mntner %s: %w", mntBy, err)
	}

	authLines := parseAllFields(mntnerData, "auth")
	for _, auth := range authLines {
		auth = strings.TrimSpace(auth)
		if strings.HasPrefix(auth, "pgp-fingerprint ") {
			fp := strings.TrimSpace(strings.TrimPrefix(auth, "pgp-fingerprint "))
			fp = strings.ReplaceAll(fp, " ", "")
			if fp != "" {
				fingerprints = append(fingerprints, fp)
			}
		}
	}

	s.cache.Put(cache.BucketRegistry, cacheKey, fingerprints, 5*time.Minute)
	registryLog.WithFields(logrus.Fields{
		"asn":          asn,
		"fingerprints": fingerprints,
	}).Debug("LookupMntnerGPGFingerprints result")

	return fingerprints, nil
}

func (s *RegistryService) FetchGPGPublicKey(fingerprint string) ([]byte, error) {
	registryLog.WithField("fingerprint", fingerprint).Debug("FetchGPGPublicKey entry")

	var cached string
	cacheKey := cache.PeerKey(cache.RegistryGPG, "pubkey:"+fingerprint)
	if ok, _ := s.cache.Get(cache.BucketRegistry, cacheKey, &cached); ok && cached != "" {
		return []byte(cached), nil
	}

	type keyserver struct {
		name string
		url  string
	}

	servers := []keyserver{
		{"keys.openpgp.org", "https://keys.openpgp.org/vks/v1/by-fingerprint/%s"},
		{"Ubuntu Keyserver", "https://keyserver.ubuntu.com/pks/lookup?op=get&search=0x%s&options=mr"},
		{"keys.mailvelope.com", "https://keys.mailvelope.com/api/v1/key?fingerprint=%s"},
	}

	var lastErr error
	for _, ks := range servers {
		u := fmt.Sprintf(ks.url, fingerprint)
		data, err := s.fetchURL(u)
		if err != nil {
			registryLog.WithError(err).WithFields(logrus.Fields{
				"fingerprint": fingerprint,
				"keyserver":   ks.name,
			}).Debug("failed to fetch pubkey from keyserver")
			lastErr = err
			continue
		}

		registryLog.WithFields(logrus.Fields{
			"fingerprint": fingerprint,
			"keyserver":   ks.name,
		}).Debug("public key fetched successfully")

		s.cache.Put(cache.BucketRegistry, cacheKey, data, 30*time.Minute)
		return []byte(data), nil
	}

	return nil, fmt.Errorf("public key not found on any keyserver (tried %d servers); please upload your public key to keys.openpgp.org or keyserver.ubuntu.com: last error: %w", len(servers), lastErr)
}

func (s *RegistryService) fetchURL(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "token "+s.token)
	}

	registryLog.WithField("url", url).Debug("fetchURL request")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	registryLog.WithFields(logrus.Fields{
		"url":    url,
		"status": resp.StatusCode,
	}).Debug("fetchURL response status")

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("not found")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func parseField(data, field string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		prefix := field + ":"
		if strings.HasPrefix(line, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			registryLog.WithFields(logrus.Fields{
				"field": field,
				"value": value,
			}).Debug("parseField found")
			return value
		}
	}
	return ""
}

func parseAllFields(data, field string) []string {
	var values []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		prefix := field + ":"
		if strings.HasPrefix(line, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			values = append(values, value)
		}
	}
	return values
}

func MaskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	domain := parts[1]
	if len(local) <= 1 {
		return local + "***@" + domain
	}
	return string(local[0]) + "***@" + domain
}
