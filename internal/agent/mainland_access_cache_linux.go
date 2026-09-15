package agent

import (
	"bufio"
	"errors"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"
)

func readMainlandRouteCache(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 || time.Since(info.ModTime()) > 24*time.Hour {
		return nil, errors.New("mainland route cache is stale")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandIPv4Ranges(file)
}

func readMainlandRouteCacheIgnoringAge(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 {
		return nil, errors.New("mainland route cache is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandIPv4Ranges(file)
}

func readMainlandDomainCache(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 || time.Since(info.ModTime()) > 24*time.Hour {
		return nil, errors.New("mainland domain cache is stale")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandACLDomainLines(file)
}

func readMainlandDomainCacheIgnoringAge(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 {
		return nil, errors.New("mainland domain cache is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandACLDomainLines(file)
}

func parseMainlandIPv4Ranges(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	result := make([]string, 0, 4096)
	seen := make(map[string]struct{}, 4096)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil || !prefix.IsValid() || !prefix.Addr().Is4() || prefix != prefix.Masked() {
			return nil, errors.New("mainland route feed contains an invalid IPv4 CIDR")
		}
		value := prefix.String()
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) < 1000 || len(result) > 20000 {
		return nil, errors.New("mainland route feed entry count is outside the safe range")
	}
	return result, nil
}

func parseMainlandDomains(reader io.Reader) ([]string, error) {
	return parseMainlandDomainLines(reader, false)
}

func parseMainlandACLDomainLines(reader io.Reader) ([]string, error) {
	return parseMainlandDomainLines(reader, true)
}

func parseMainlandDomainLines(reader io.Reader, aclFormat bool) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	result := make([]string, 0, 6000)
	seen := make(map[string]struct{}, 6000)
	domainPattern := regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+$`)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value := ""
		if aclFormat {
			domain := ""
			if strings.HasPrefix(line, "||") && !strings.HasPrefix(line, "|||") {
				domain = strings.TrimPrefix(line, "||")
			} else if strings.HasPrefix(line, "|") && !strings.HasPrefix(line, "||") {
				domain = strings.TrimPrefix(line, "|")
			}
			if domainPattern.MatchString(domain) {
				value = line
			}
		} else if strings.HasPrefix(line, "+.") && domainPattern.MatchString(strings.TrimPrefix(line, "+.")) {
			value = "||" + strings.TrimPrefix(line, "+.")
		} else if domainPattern.MatchString(line) {
			value = "|" + line
		}
		if value == "" {
			return nil, errors.New("mainland domain feed contains an invalid domain")
		}
		value = strings.ToLower(value)
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) < 1000 || len(result) > 50000 {
		return nil, errors.New("mainland domain feed entry count is outside the safe range")
	}
	return result, nil
}
