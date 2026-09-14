package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func containsEngine(values []core.Engine, expected core.Engine) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsFeature(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func intervalString(duration time.Duration) string {
	seconds := int64(duration.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func truncate(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "�") + "\n… output truncated by QControlHub"
}
