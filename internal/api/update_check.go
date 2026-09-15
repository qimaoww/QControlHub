package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (s *Server) checkUpdate(w http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
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
	latest, publishedAt, err := latestImageVersion(ctx, client)
	if err != nil {
		writeError(w, http.StatusBadGateway, "检查 GHCR 最新镜像失败")
		return
	}
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
		"published_at":          publishedAt,
		"source":                "ghcr-latest",
		"comparable":            comparable,
		"update_available":      available,
	})
}
