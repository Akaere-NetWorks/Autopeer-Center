package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

func safePathRe() *regexp.Regexp {
	return regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
}

func (h *AdminHandler) ListReleases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	releases, err := h.releases.List(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to list releases")
		return
	}

	type releaseResp struct {
		Version    string `json:"version"`
		OS         string `json:"os"`
		Arch       string `json:"arch"`
		SHA256     string `json:"sha256"`
		Size       int64  `json:"size"`
		UploadedBy string `json:"uploaded_by"`
		UploadedAt string `json:"uploaded_at"`
	}
	result := make([]releaseResp, 0, len(releases))
	for _, rel := range releases {
		uploadedBy := ""
		if rel.UploadedBy != nil {
			uploadedBy = *rel.UploadedBy
		}
		result = append(result, releaseResp{
			Version:    rel.Version,
			OS:         rel.OS,
			Arch:       rel.Arch,
			SHA256:     rel.SHA256,
			Size:       rel.Size,
			UploadedBy: uploadedBy,
			UploadedAt: rel.UploadedAt.UTC().Format(time.RFC3339),
		})
	}

	adminLog.WithField("count", len(result)).Debug("ListReleases returning results")

	JSON(w, http.StatusOK, map[string]interface{}{"releases": result})
}

func (h *AdminHandler) UploadRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	version := r.FormValue("version")
	osName := r.FormValue("os")
	arch := r.FormValue("arch")
	if version == "" || osName == "" || arch == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "version, os, arch are required")
		return
	}

	safePathRe := safePathRe()
	if !safePathRe.MatchString(version) || !safePathRe.MatchString(osName) || !safePathRe.MatchString(arch) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "invalid path component")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	file, header, err := r.FormFile("binary")
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			ErrorJSON(w, r, http.StatusRequestEntityTooLarge, "too_large", "Binary file exceeds 100 MB limit")
			return
		}
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "binary file is required")
		return
	}
	defer file.Close()

	releaseDir := config.Get().AgentReleaseDir
	if releaseDir == "" {
		releaseDir = "/var/lib/autopeer-center/releases"
	}
	destDir := filepath.Join(releaseDir, version, osName, arch)
	if !strings.HasPrefix(filepath.Clean(destDir)+string(os.PathSeparator),
		filepath.Clean(releaseDir)+string(os.PathSeparator)) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "invalid path")
		return
	}
	os.MkdirAll(destDir, 0755)

	destPath := filepath.Join(destDir, "autopeer-agent")
	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to write release file")
		return
	}

	hasher := sha256.New()
	tr := io.TeeReader(file, hasher)
	written, err := io.Copy(f, tr)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to write release file")
		return
	}
	f.Close()

	sha256Hex := hex.EncodeToString(hasher.Sum(nil))

	adminLog.WithFields(logrus.Fields{
		"version": version, "os": osName, "arch": arch, "size": written, "sha256": sha256Hex,
	}).Debug("UploadRelease entry")

	if _, err := h.releases.GetByVersion(ctx, version, osName, arch); err == nil {
		os.Remove(tmpPath)
		ErrorJSON(w, r, http.StatusConflict, "version_exists", "This version already exists. Delete it first before re-uploading.")
		return
	}

	if err := h.releases.Create(ctx, &model.AgentRelease{
		Version:    version,
		OS:         osName,
		Arch:       arch,
		SHA256:     sha256Hex,
		Size:       written,
		Path:       destPath,
		UploadedBy: &adminEmail,
	}); err != nil {
		os.Remove(tmpPath)
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to save release record")
		return
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		adminLog.WithError(err).Error("UploadRelease: failed to rename temp file to final path")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to finalize release file")
		return
	}

	h.auditSvc.Log(ctx, "release.upload", adminEmail, nil, map[string]interface{}{
		"version": version, "os": osName, "arch": arch, "size": header.Size,
	})

	adminLog.WithFields(logrus.Fields{"version": version, "os": osName, "arch": arch}).Info("release uploaded successfully")

	JSON(w, http.StatusCreated, map[string]interface{}{
		"version": version, "os": osName, "arch": arch, "sha256": sha256Hex, "size": written,
	})
}

func (h *AdminHandler) DeleteRelease(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	version := chi.URLParam(r, "version")
	osName := r.URL.Query().Get("os")
	arch := r.URL.Query().Get("arch")
	if osName == "" {
		osName = "linux"
	}
	if arch == "" {
		arch = "amd64"
	}

	re := safePathRe()
	if !re.MatchString(version) || !re.MatchString(osName) || !re.MatchString(arch) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid version, os, or arch")
		return
	}

	adminLog.WithFields(logrus.Fields{"version": version, "os": osName, "arch": arch}).Debug("DeleteRelease entry")

	filePath, err := h.releases.Delete(ctx, version, osName, arch)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Release not found")
		return
	}

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		adminLog.WithError(err).WithField("path", filePath).Warn("release record deleted but file removal failed")
	}

	h.auditSvc.Log(ctx, "release.delete", adminEmail, nil, map[string]interface{}{"version": version})

	adminLog.WithFields(logrus.Fields{"version": version, "os": osName, "arch": arch}).Info("release deleted")

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *AdminHandler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	var body struct {
		Version string `json:"version"`
		OS      string `json:"os"`
		Arch    string `json:"arch"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Version == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "version is required")
		return
	}
	if body.OS == "" {
		body.OS = "linux"
	}
	if body.Arch == "" {
		body.Arch = "amd64"
	}

	rel, err := h.releases.GetByVersion(ctx, body.Version, body.OS, body.Arch)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Release not found")
		return
	}

	updateID := uuid.New().String()
	downloadURL := fmt.Sprintf("%s/api/v1/agent/download?version=%s&os=%s&arch=%s",
		config.Get().ExternalURL, body.Version, body.OS, body.Arch)

	adminLog.WithFields(logrus.Fields{
		"node_id": nodeID, "version": body.Version, "download_url": downloadURL,
	}).Debug("UpdateAgent sending command")

	resp, err := h.hub.SendCommand(nodeID, ws.TypeAgentUpdate, map[string]string{
		"update_id": updateID,
		"version":   body.Version,
		"url":       downloadURL,
		"sha256":    rel.SHA256,
	})
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Agent not reachable: "+err.Error())
		return
	}

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "version": body.Version, "success": resp.Success}).Debug("agent update response")

	if resp.Success != nil && !*resp.Success {
		h.auditSvc.Log(ctx, "agent.update.failed", adminEmail, &nodeID, map[string]interface{}{
			"version": body.Version, "update_id": updateID, "error": resp.Error,
		})
		ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent update failed: "+resp.Error)
		return
	}

	h.auditSvc.Log(ctx, "agent.update.success", adminEmail, &nodeID, map[string]interface{}{
		"version": body.Version, "update_id": updateID,
	})

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "version": body.Version}).Info("agent update initiated")

	JSON(w, http.StatusOK, map[string]interface{}{
		"status":    "initiated",
		"version":   body.Version,
		"update_id": updateID,
	})
}

func (h *AdminHandler) RollbackAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	adminLog.WithField("node_id", nodeID).Debug("RollbackAgent entry")

	resp, err := h.hub.SendCommand(nodeID, ws.TypeAgentRollback, nil)
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Agent not reachable: "+err.Error())
		return
	}

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "success": resp.Success}).Debug("agent rollback response")

	if resp.Success != nil && !*resp.Success {
		h.auditSvc.Log(ctx, "agent.rollback.failed", adminEmail, &nodeID, map[string]interface{}{"error": resp.Error})
		ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Rollback failed: "+resp.Error)
		return
	}

	h.auditSvc.Log(ctx, "agent.rollback", adminEmail, &nodeID, nil)

	adminLog.WithField("node_id", nodeID).Info("agent rollback initiated")

	JSON(w, http.StatusOK, map[string]string{"status": "rollback initiated"})
}
