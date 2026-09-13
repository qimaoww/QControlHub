package core

import (
	"errors"
	"net/url"
	"strings"
)

const AgentFeatureCNIPSource = "cnip-source-v1"

type CNIPSource struct {
	URL     string `json:"ipv4_url"`
	IPv6URL string `json:"ipv6_url"`
	Format  string `json:"format"`
}

func (source CNIPSource) Validate() error {
	if source.IPv6URL != "" {
		if err := (CNIPSource{URL: source.IPv6URL, Format: source.Format}).Validate(); err != nil {
			return err
		}
	}
	if source.URL == "" {
		return nil
	}
	u, err := url.Parse(source.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || len(source.URL) > 2000 {
		return errors.New("CN IP 数据源须为 HTTPS 直链，不支持认证信息、查询参数或片段")
	}
	switch source.Format {
	case "", "auto", "txt", "dat", "srs", "mmdb":
	default:
		return errors.New("CN IP 格式须为自动、TXT、DAT、SRS 或 MMDB")
	}
	if strings.ContainsAny(source.URL, "\r\n") {
		return errors.New("CN IP 数据源地址无效")
	}
	return nil
}

func (source CNIPSource) Custom() bool { return source.URL != "" || source.IPv6URL != "" }
