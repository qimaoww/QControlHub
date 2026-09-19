package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func newIPQualityArchiveClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for _, ip := range addresses {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				return nil, errors.New("IPQuality report host resolved to a non-public address")
			}
		}
		for _, ip := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last == nil {
			last = errors.New("IPQuality report host has no addresses")
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("IPQuality report redirects are disabled")
		},
	}
}

// Download in memory before entering the completion transaction. No filesystem
// copy, ambient proxy, caller-selected host, or browser fetch from upstream.
func downloadIPQualityArchive(ctx context.Context, client *http.Client, link string, family int) (core.IPQualityArchive, error) {
	if !core.ValidIPQualityReportURL(link) {
		return core.IPQualityArchive{}, errors.New("IPQuality 报告链接无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return core.IPQualityArchive{}, err
	}
	request.Header.Set("Accept", "image/svg+xml")
	response, err := client.Do(request)
	if err != nil {
		return core.IPQualityArchive{}, errors.New("IPQuality 报告下载失败，请检查面板到上游的连接")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return core.IPQualityArchive{}, fmt.Errorf("IPQuality 报告下载失败：HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, core.MaxIPQualityArchiveBytes+1))
	if err != nil || len(content) > core.MaxIPQualityArchiveBytes {
		return core.IPQualityArchive{}, errors.New("IPQuality 报告读取失败或超过 2 MiB")
	}
	if err := validateIPQualitySVG(content); err != nil {
		return core.IPQualityArchive{}, err
	}
	digest := sha256.Sum256(content)
	return core.IPQualityArchive{IPQualityArchiveInfo: core.IPQualityArchiveInfo{
		Family: family, SourceURL: link, Size: len(content), SHA256: hex.EncodeToString(digest[:]), DownloadedAt: time.Now().UTC(),
	}, Content: content}, nil
}

func validateIPQualitySVG(content []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) && roots == 1 && depth == 0 {
			return nil
		}
		if err != nil {
			return errors.New("IPQuality 下载内容不是完整的 SVG 报告")
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || token.Name.Local != "svg" || token.Name.Space != "http://www.w3.org/2000/svg" {
					return errors.New("IPQuality 下载内容不是 SVG 报告")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
				return errors.New("IPQuality SVG 包含无效内容")
			}
		}
	}
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
			if err == nil && len(report.ReportURLs) == 0 {
				err = errors.New("Agent 未返回报告下载链接，请升级 Agent 后重新检测")
			}
			if err == nil {
				client := s.ipQualityArchiveHTTP
				if client == nil {
					client = newIPQualityArchiveClient()
					defer client.CloseIdleConnections()
				}
				for i, link := range report.ReportURLs {
					var archive core.IPQualityArchive
					archive, err = downloadIPQualityArchive(ctx, client, link, core.IPQualityReportFamily(report.Reports[i]))
					if err != nil {
						break
					}
					archives = append(archives, archive)
				}
			}
			if err != nil {
				result.Success, result.Output, result.Error = false, "", "IPQuality 报告未存档："+err.Error()
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
