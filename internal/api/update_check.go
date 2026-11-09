package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) checkUpdate(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	result, err := s.refreshNotificationUpdate(ctx, true)
	if err != nil {
		writeError(w, http.StatusBadGateway, "检查 GHCR 最新镜像失败")
		return
	}
	latest := result.Version
	available, comparable := imageVersionStatus(s.controlPlaneVersion, latest)
	displayLatest := latest
	if commitVersionPattern.MatchString(strings.ToLower(latest)) && len(latest) > 7 {
		displayLatest = latest[:7]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current_control_plane": s.controlPlaneVersion,
		"current_agent_package": s.agentVersion,
		"latest_version":        displayLatest,
		"latest_revision":       latest,
		"release_url":           "https://github.com/qimaoww/qcontrolhub/pkgs/container/qcontrol-plane",
		"checked_at":            result.CheckedAt,
		"published_at":          result.PublishedAt,
		"source":                "ghcr-latest",
		"comparable":            comparable,
		"update_available":      available,
	})
}

func (s *Server) refreshNotificationUpdate(ctx context.Context, manual bool) (store.NotificationUpdate, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(next *http.Request, previous []*http.Request) error {
			if len(previous) >= 5 {
				return errors.New("too many container registry redirects")
			}
			host := strings.ToLower(next.URL.Hostname())
			if next.URL.Scheme != "https" || (host != "ghcr.io" && host != "pkg-containers.githubusercontent.com") {
				return errors.New("container registry redirect was rejected")
			}
			return nil
		},
	}

	result, err := s.store.RefreshNotificationUpdate(ctx, manual, func(ctx context.Context) (string, time.Time, error) {
		return latestImageVersion(ctx, client)
	})
	if err != nil {
		return result, err
	}
	if result.Error != "" || result.Version == "" {
		return result, errors.New("GHCR check unavailable")
	}
	available, _ := imageVersionStatus(s.controlPlaneVersion, result.Version)
	err = s.store.SetNotificationUpdateComparison(ctx, result.Version, available)
	return result, err
}
