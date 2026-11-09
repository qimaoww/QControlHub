package serverconfig

import (
	"encoding/json"
	"fmt"
)

// These are server listener fields in Mihomo v1.19.33, not outbound options.
var mihomoXHTTPFields = map[string]string{
	"xPaddingBytes": "x-padding-bytes", "xPaddingObfsMode": "x-padding-obfs-mode", "xPaddingKey": "x-padding-key", "xPaddingHeader": "x-padding-header", "xPaddingPlacement": "x-padding-placement", "xPaddingMethod": "x-padding-method",
	"uplinkHTTPMethod": "uplink-http-method", "sessionPlacement": "session-placement", "sessionKey": "session-key", "seqPlacement": "seq-placement", "seqKey": "seq-key", "uplinkDataPlacement": "uplink-data-placement", "uplinkDataKey": "uplink-data-key", "uplinkChunkSize": "uplink-chunk-size", "noSSEHeader": "no-sse-header", "scStreamUpServerSecs": "sc-stream-up-server-secs", "scMaxBufferedPosts": "sc-max-buffered-posts", "scMaxEachPostBytes": "sc-max-each-post-bytes",
}

func mihomoXHTTPConfig(input Input) (map[string]any, error) {
	config := map[string]any{"path": input.TransportPath, "host": input.TransportHost, "mode": xhttpServerMode(input)}
	extra, err := xhttpObject(input.XHTTPServerExtra)
	if err != nil {
		return nil, err
	}
	for key, value := range extra {
		field, ok := mihomoXHTTPFields[key]
		if !ok {
			return nil, fmt.Errorf("Mihomo 服务端不支持 XHTTP 参数 %s", key)
		}
		if key == "noSSEHeader" || key == "xPaddingObfsMode" {
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("XHTTP %s 必须为布尔值", key)
			}
			config[field] = value
		} else {
			switch value.(type) {
			case string, float64, int, json.Number:
				config[field] = fmt.Sprint(value)
			default:
				return nil, fmt.Errorf("XHTTP %s 必须为字符串或数字", key)
			}
		}
	}
	return config, nil
}

func parseMihomoXHTTPExtra(config map[string]any) string {
	extra := map[string]any{}
	for key, field := range mihomoXHTTPFields {
		if value, ok := config[field]; ok {
			extra[key] = value
		}
	}
	if len(extra) == 0 {
		return ""
	}
	data, _ := json.Marshal(extra)
	return string(data)
}
