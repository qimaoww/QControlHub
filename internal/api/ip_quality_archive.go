package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// renderIPQualityArchives draws each stored report into the SVG the panel
// serves. Nothing is fetched from upstream, so a valid report always archives
// and detection no longer depends on an upload endpoint.
func renderIPQualityArchives(result *core.IPQualityResult) ([]core.IPQualityArchive, error) {
	archives := make([]core.IPQualityArchive, 0, len(result.Reports))
	for index, report := range result.Reports {
		if index >= len(result.ReportsText) || strings.TrimSpace(result.ReportsText[index]) == "" {
			return nil, errors.New("Agent 未返回报告原文，请升级 Agent 后重新检测")
		}
		content, err := renderIPQualitySVG(result.ReportsText[index])
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(content)
		archives = append(archives, core.IPQualityArchive{
			IPQualityArchiveInfo: core.IPQualityArchiveInfo{
				Family:     core.IPQualityReportFamily(report),
				SHA256:     hex.EncodeToString(digest[:]),
				Size:       len(content),
				RenderedAt: time.Now().UTC(),
			},
			Content: content,
		})
	}
	return archives, nil
}

func (s *Server) completeAgentTask(ctx context.Context, agentID, taskID string, result *core.TaskResultRequest) error {
	var archives []core.IPQualityArchive
	if result.Success && result.IPQuality != nil {
		allowed, err := s.store.CanArchiveIPQualityResult(ctx, agentID, taskID, result.LeaseID)
		if err != nil {
			return err
		}
		if allowed {
			report, err := core.NormalizeIPQualityResult(result.IPQuality)
			if err == nil {
				archives, err = renderIPQualityArchives(&report)
			}
			if err != nil {
				result.Success, result.Output, result.Error = false, "", "IPQuality 报告未生成存档图："+err.Error()
				archives = nil
			}
		}
	}
	return s.store.CompleteTask(ctx, agentID, taskID, *result, archives...)
}

func (s *Server) getIPQualityArchive(w http.ResponseWriter, request *http.Request) {
	family, err := strconv.Atoi(request.PathValue("family"))
	if err != nil || (family != 4 && family != 6) {
		writeError(w, http.StatusBadRequest, "family must be 4 or 6")
		return
	}
	archive, err := s.store.GetIPQualityArchive(request.Context(), request.PathValue("id"), family)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	disposition := "inline"
	if request.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="ip-quality-ipv%d.svg"`, disposition, family))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(archive.Content)))
	_, _ = w.Write(archive.Content)
}
