package agent

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const maxPlatformNameRunes = 50

var (
	platformOnce sync.Once
	platformName string
)

// operatingSystemPlatform reports the Linux distribution name instead of the
// generic GOOS value when /etc/os-release is available. Versions and release
// codenames are deliberately omitted so the compact platform label stays
// stable across OS upgrades. The result is cached because it is included in
// every heartbeat but does not change while the Agent is running.
func operatingSystemPlatform() string {
	platformOnce.Do(func() {
		platformName = runtime.GOOS
		if runtime.GOOS != "linux" {
			return
		}
		content, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return
		}
		if detected := operatingSystemPlatformFromRelease(content); detected != "" {
			platformName = detected
		}
	})
	return platformName
}

func operatingSystemPlatformFromRelease(content []byte) string {
	values := make(map[string]string)
	for _, rawLine := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "PRETTY_NAME" && key != "NAME" && key != "ID" {
			continue
		}
		values[key] = decodeOSReleaseValue(value)
	}
	switch strings.ToLower(sanitizePlatformName(values["ID"])) {
	case "debian":
		return "Debian"
	case "alpine":
		return "Alpine"
	}
	if name := sanitizePlatformName(values["NAME"]); name != "" {
		return name
	}
	if identifier := sanitizePlatformName(values["ID"]); identifier != "" {
		return identifier
	}
	return sanitizePlatformName(values["PRETTY_NAME"])
}

func decodeOSReleaseValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return value
	}
	quote := value[0]
	if (quote != '\'' && quote != '"') || value[len(value)-1] != quote {
		return value
	}
	value = value[1 : len(value)-1]
	if quote == '\'' {
		return value
	}
	var decoded strings.Builder
	decoded.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) && strings.ContainsRune("$\"\\`", rune(value[i+1])) {
			i++
		}
		decoded.WriteByte(value[i])
	}
	return decoded.String()
}

func sanitizePlatformName(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maxPlatformNameRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxPlatformNameRunes]))
}
