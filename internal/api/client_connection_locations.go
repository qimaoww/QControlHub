package api

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const (
	clientLocationLookupTimeout = 3 * time.Second
	clientLocationWorkers       = 8
	clientLocationNegativeTTL   = 5 * time.Minute
	clientLocationProvinceTTL   = 6 * time.Hour
	clientLocationKnownTTL      = 48 * time.Hour
)

// Enrichment happens after authorization and never blocks Agent ingestion.
// One shared deadline bounds cold-cache latency regardless of page size.
func (s *Server) resolveClientConnectionLocations(ctx context.Context, records []core.ClientConnectionRecord, cacheOnly bool) {
	ips := make([]string, 0, len(records))
	seen := map[string]bool{}
	for i := range records {
		address, err := netip.ParseAddr(records[i].ClientIP)
		if err != nil {
			continue
		}
		if !netpolicy.IsPublicAddress(address) {
			records[i].Location.NonPublic = true
			continue
		}
		ip := address.Unmap().String()
		if !seen[ip] {
			ips = append(ips, ip)
			seen[ip] = true
		}
	}
	if len(ips) == 0 {
		return
	}
	cached, err := s.store.ClientConnectionLocations(ctx, ips)
	if err != nil {
		slog.Warn("read client IP locations", "error", err)
		return
	}
	for i := range records {
		if item, ok := cached[records[i].ClientIP]; ok {
			records[i].Location = item.ClientIPLocation
			records[i].LocationRetryAt = &item.RetryAfter
		}
	}
	if cacheOnly {
		return
	}
	now := time.Now().UTC()
	lookupCtx, cancel := context.WithTimeout(ctx, clientLocationLookupTimeout)
	defer cancel()
	jobs := make(chan string, len(ips))
	for _, ip := range ips {
		if item, ok := cached[ip]; needsClientLocationLookup(item, ok, now) {
			jobs <- ip
		}
	}
	close(jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	updates := make([]store.ClientConnectionLocation, 0, len(ips))
	for range min(clientLocationWorkers, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if lookupCtx.Err() != nil {
					return
				}
				region, err := s.geoip.Lookup(lookupCtx, netip.MustParseAddr(ip))
				item := store.ClientConnectionLocation{IP: ip, RetryAfter: now.Add(clientLocationNegativeTTL)}
				if err == nil {
					item.ClientIPLocation = core.ClientIPLocation{CountryCode: region.ISOCode, Country: region.Name, Province: region.Province}
					if region.ISOCode == "CN" && region.Province == "" {
						item.RetryAfter = now.Add(clientLocationProvinceTTL)
					} else {
						item.RetryAfter = now.Add(clientLocationKnownTTL)
					}
				}
				mu.Lock()
				updates = append(updates, item)
				if item.CountryCode == "" {
					item.ClientIPLocation = cached[ip].ClientIPLocation
				}
				cached[ip] = item
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if err := s.store.SaveClientConnectionLocations(ctx, updates); err != nil {
		slog.Warn("save client IP locations", "error", err)
	}
	for i := range records {
		if item, ok := cached[records[i].ClientIP]; ok {
			records[i].Location = item.ClientIPLocation
			records[i].LocationRetryAt = &item.RetryAfter
		}
	}
}

func needsClientLocationLookup(item store.ClientConnectionLocation, cached bool, now time.Time) bool {
	if !cached || !now.Before(item.RetryAfter) {
		return true
	}
	// Entries written before province fallbacks used a 48-hour TTL even when
	// their CN province was empty. Refresh them once after deployment.
	return item.CountryCode == "CN" && item.Province == "" && item.RetryAfter.Sub(now) > clientLocationProvinceTTL
}

// Read only cooldowns for returned IPs with incomplete geography. On an
// unchanged sync response there are no records and no lookup query to make.
func (s *Server) clientConnectionRetryTimes(ctx context.Context, records []core.ClientConnectionRecord) {
	ips := make([]string, 0, len(records))
	seen := make(map[string]bool)
	for _, record := range records {
		l := record.Location
		if !l.NonPublic && (l.CountryCode == "" || l.CountryCode == "CN" && l.Province == "") && !seen[record.ClientIP] {
			seen[record.ClientIP] = true
			ips = append(ips, record.ClientIP)
		}
	}
	if len(ips) == 0 {
		return
	}
	cooldowns, err := s.store.ClientConnectionLocations(ctx, ips)
	if err != nil {
		slog.Debug("read client location cooldowns", "error", err)
		return
	}
	for i := range records {
		if item, ok := cooldowns[records[i].ClientIP]; ok && !needsClientLocationLookup(item, true, time.Now().UTC()) {
			records[i].LocationRetryAt = &item.RetryAfter
		}
	}
}
