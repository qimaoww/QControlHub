package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const ipQualityRenderFixture = `{"Head":{"IP":"203.0.113.1","Time":"2026-09-19 13:56:33 UTC","Version":"v2026-09-16"},` +
	`"Info":{"ASN":"210634","Organization":"YSTRONTEK NETWORKS","DMS":"114°10′29″E","Map":"https://check.place/1,2,12,en",` +
	`"TimeZone":"Asia/Hong_Kong","City":{"Name":"香港","PostalCode":"999077"},"Region":{"Code":"HK","Name":"香港"},` +
	`"Continent":{"Code":"AS","Name":"亚洲"},"RegisteredRegion":{"Code":"US","Name":"美国"},"Type":"广播IP"},` +
	`"Type":{"Usage":{"IPinfo":"机房","ipapi":"商业"},"Company":{"IPinfo":"机房"}},` +
	`"Score":{"IP2LOCATION":"3","ipapi":"5.47%","IPQS":"null"},` +
	`"Factor":{"CountryCode":{"IPinfo":"HK","ipapi":"HK"},"Proxy":{"IPinfo":false,"ipapi":true}},` +
	`"Media":{"TikTok":{"Status":"解锁","Region":"ALISG","Type":"原生"},"ChatGPT":{"Status":"仅APP","Region":"HK","Type":"DNS"}},` +
	`"Mail":{"Port25":true,"Gmail":true,"QQ":false,"DNSBlacklist":{"Total":423,"Clean":404,"Marked":18,"Blacklisted":1}}}`

func TestIPQualityRenderDrawsStoredReport(t *testing.T) {
	archives, err := renderIPQualityArchives(&core.IPQualityResult{Reports: []json.RawMessage{json.RawMessage(ipQualityRenderFixture)}})
	if err != nil || len(archives) != 1 {
		t.Fatalf("render: %+v %v", archives, err)
	}
	archive := archives[0]
	if archive.Family != 4 || archive.Size != len(archive.Content) || len(archive.SHA256) != 64 || archive.RenderedAt.IsZero() {
		t.Fatalf("incomplete archive metadata: %+v", archive)
	}
	digest := sha256.Sum256(archive.Content)
	if archive.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("archive digest does not match its content")
	}
	content := string(archive.Content)
	for _, want := range []string{
		"<svg", "IP质量体检报告：203.0.113.1", "脚本版本：v2026-09-16", "AS210634", "YSTRONTEK NETWORKS",
		"[HK]香港", "广播IP", "机房", "商业", "低风险", "高风险", "解锁", "仅APP", "DNS",
		"Gmail", "已标记 ", "黑名单 ", "风险等级：", "有效 ",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered report is missing %q", want)
		}
	}
	// The panel must not carry an upstream report link any more.
	if strings.Contains(content, "report.check.place") {
		t.Fatal("rendered report still references the upstream report host")
	}
	if err := validateRenderedSVG([]byte(content)); err != nil {
		t.Fatalf("rendered report is not a single SVG document: %v", err)
	}
}

func TestIPQualityRenderEscapesAndRejectsBadReports(t *testing.T) {
	hostile := `{"Head":{"IP":"203.0.113.1"},"Info":{"Organization":"<script>alert(1)</script>"},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`
	archives, err := renderIPQualityArchives(&core.IPQualityResult{Reports: []json.RawMessage{json.RawMessage(hostile)}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(archives[0].Content), "<script>") {
		t.Fatal("provider text was not escaped")
	}
	for _, raw := range []string{`not json`, `{"Info":{}}`, `{"Head":{"IP":""}}`, `[]`} {
		if _, err := renderIPQualitySVG(json.RawMessage(raw), nil); err == nil {
			t.Fatalf("accepted malformed report %q", raw)
		}
	}
}

func TestIPQualityRenderAssignsAddressFamilies(t *testing.T) {
	reports := []json.RawMessage{
		json.RawMessage(`{"Head":{"IP":"203.0.113.1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`),
		json.RawMessage(`{"Head":{"IP":"2001:db8::1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`),
	}
	archives, err := renderIPQualityArchives(&core.IPQualityResult{Reports: reports})
	if err != nil || len(archives) != 2 {
		t.Fatalf("render: %+v %v", archives, err)
	}
	if archives[0].Family != 4 || archives[1].Family != 6 {
		t.Fatalf("families = %d, %d", archives[0].Family, archives[1].Family)
	}
}

// validateRenderedSVG mirrors what a browser needs: exactly one SVG root.
func validateRenderedSVG(content []byte) error {
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) && roots == 1 && depth == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || token.Name.Local != "svg" || token.Name.Space != "http://www.w3.org/2000/svg" {
					return errors.New("not a single svg root")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}
