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
		}
	}
	if cacheOnly {
		return
	}
	now := time.Now().UTC()
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	jobs := make(chan string, len(ips))
	for _, ip := range ips {
		if item, ok := cached[ip]; !ok || !now.Before(item.RetryAfter) {
			jobs <- ip
		}
	}
	close(jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	updates := make([]store.ClientConnectionLocation, 0, len(ips))
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if lookupCtx.Err() != nil {
					return
				}
				region, err := s.geoip.Lookup(lookupCtx, netip.MustParseAddr(ip))
				item := store.ClientConnectionLocation{IP: ip, RetryAfter: now.Add(5 * time.Minute)}
				if err == nil {
					item.ClientIPLocation = core.ClientIPLocation{CountryCode: region.ISOCode, Country: region.Name, Province: region.Province}
					item.RetryAfter = now.Add(48 * time.Hour)
				}
				mu.Lock()
				if err == nil {
					cached[ip] = item
				}
				updates = append(updates, item)
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
		}
	}
}
