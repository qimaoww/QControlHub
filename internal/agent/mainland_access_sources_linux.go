package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (manager *MainlandAccessManager) loadRoutes(ctx context.Context) ([]string, error) {
	if cached, err := readMainlandRouteCache(manager.cachePath); err == nil && len(cached) > 0 {
		return cached, nil
	}
	fallback := func(cause error) ([]string, error) {
		if cached, err := readMainlandRouteCacheIgnoringAge(manager.cachePath); err == nil && len(cached) > 0 {
			return cached, nil
		}
		return nil, cause
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, mainlandIPv4URL, nil)
	if err != nil {
		return fallback(err)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fallback(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fallback(fmt.Errorf("mainland route feed returned HTTP %d", response.StatusCode))
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 512<<10+1))
	if err != nil || len(contents) > 512<<10 {
		return fallback(errors.New("mainland route feed is unavailable or too large"))
	}
	ranges, err := parseMainlandIPv4Ranges(strings.NewReader(string(contents)))
	if err != nil {
		return fallback(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.cachePath), 0o700); err == nil {
		_ = os.WriteFile(manager.cachePath, []byte(strings.Join(ranges, "\n")+"\n"), 0o600)
	}
	return ranges, nil
}

func (manager *MainlandAccessManager) loadDomains(ctx context.Context) ([]string, error) {
	if cached, err := readMainlandDomainCache(manager.domainPath); err == nil && len(cached) > 0 {
		return cached, nil
	}
	fallback := func(cause error) ([]string, error) {
		if cached, err := readMainlandDomainCacheIgnoringAge(manager.domainPath); err == nil && len(cached) > 0 {
			return cached, nil
		}
		return nil, cause
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, mainlandDomainURL, nil)
	if err != nil {
		return fallback(err)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fallback(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fallback(fmt.Errorf("mainland domain feed returned HTTP %d", response.StatusCode))
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 512<<10+1))
	if err != nil || len(contents) > 512<<10 {
		return fallback(errors.New("mainland domain feed is unavailable or too large"))
	}
	domains, err := parseMainlandDomains(strings.NewReader(string(contents)))
	if err != nil {
		return fallback(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.domainPath), 0o700); err == nil {
		_ = os.WriteFile(manager.domainPath, []byte(strings.Join(domains, "\n")+"\n"), 0o600)
	}
	return domains, nil
}
