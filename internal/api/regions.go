package api

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const (
	// regionResolveBudget bounds how long a node list waits for automatic
	// region flags. Lookups that outlive it keep running into the GeoIP cache,
	// so a slow provider delays one read instead of every navigation.
	regionResolveBudget = time.Second
	// regionResolveConcurrency keeps a large fleet from opening one outbound
	// request per node at the same time.
	regionResolveConcurrency = 8
)

type regionLookup struct {
	index   int
	address netip.Addr
}

type regionLookupResult struct {
	index int
	code  string
}

func agentGeoIP(agent core.Agent) netip.Addr {
	for _, raw := range []string{
		agent.Metrics.PublicIPv4,
		agent.Metrics.PublicIPv6,
		agent.Metrics.ObservedPublicIP,
	} {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || !netpolicy.IsPublicAddress(address) || netpolicy.IsCloudflareAddress(address) {
			continue
		}
		return address
	}
	for _, networkInterface := range agent.Metrics.NetworkInterfaces {
		for _, raw := range networkInterface.Addresses {
			address, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil || !netpolicy.IsPublicAddress(address) || netpolicy.IsCloudflareAddress(address) {
				continue
			}
			return address
		}
	}
	return netip.Addr{}
}

func (s *Server) getAgentRegion(w http.ResponseWriter, request *http.Request) {
	agent, err := s.store.GetAgent(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if code := store.AgentRegionCode(agent); code != "" {
		writeJSON(w, http.StatusOK, map[string]string{"country_code": code, "source": "manual"})
		return
	}
	// Automatic detection derives the address and country from host metrics,
	// which metrics.read gates. Without the capability this endpoint must stay
	// empty instead of disclosing the node address or its GeoIP.
	if !s.sessionAllows(request, core.PermissionMetricsRead) {
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	address := agentGeoIP(agent)
	if !address.IsValid() {
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	region, err := s.geoip.LookupCountry(request.Context(), address)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"ip":           address.String(),
		"country_code": region.ISOCode,
		"country":      region.Name,
		"source":       "auto",
	})
}

func (s *Server) listRegions(w http.ResponseWriter, request *http.Request) {
	writeJSON(w, http.StatusOK, geoip.RegionCodes())
}

func (s *Server) putAgentRegion(w http.ResponseWriter, request *http.Request) {
	var input struct {
		CountryCode *string `json:"country_code"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.CountryCode == nil {
		writeError(w, http.StatusBadRequest, "country_code is required; use an empty string for automatic GeoIP")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(*input.CountryCode))
	if err := s.store.SetAgentRegionCode(request.Context(), request.PathValue("id"), code); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.region.updated", request.PathValue("id"), code)
	writeJSON(w, http.StatusOK, map[string]string{"country_code": code})
}

func (s *Server) getRegionFlag(w http.ResponseWriter, request *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(request.PathValue("code")))
	// The panel display policy treats Taiwan as part of China and uses the
	// China flag regardless of which code a direct API caller supplies.
	if code == "TW" {
		code = "CN"
	}
	flag, err := s.geoip.Flag(request.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=172800, immutable")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(flag)
}

// resolveAgentRegions attaches the display region to every node in a list
// response, so a node grid renders its flags without one request per card.
// Manual preferences always win; automatic detection reuses the GeoIP client's
// cache, and a provider failure only leaves that node without a flag instead of
// failing the whole page.
//
// Lookups run concurrently under one budget because every panel page loads this
// list: one serial provider round trip per node turned a blocked or slow
// provider into a multi-second stall on each navigation.
func (s *Server) resolveAgentRegions(request *http.Request, agents []core.Agent) {
	auto := s.sessionAllows(request, core.PermissionMetricsRead)
	lookups := make([]regionLookup, 0, len(agents))
	for index := range agents {
		if code := store.AgentRegionCode(agents[index]); code != "" {
			agents[index].RegionCode = code
			continue
		}
		if !auto {
			continue
		}
		address := agentGeoIP(agents[index])
		if !address.IsValid() {
			continue
		}
		lookups = append(lookups, regionLookup{index: index, address: address})
	}
	if len(lookups) == 0 {
		return
	}

	// Detach from the request context: work that outlives the budget below must
	// still reach the GeoIP cache, otherwise the next navigation repeats the
	// same failing provider round trip.
	lookupContext := context.WithoutCancel(request.Context())
	deadline := time.Now().Add(regionResolveBudget)
	results := make(chan regionLookupResult, len(lookups))
	permits := make(chan struct{}, regionResolveConcurrency)
	for _, lookup := range lookups {
		if !acquireRegionPermit(permits, deadline) {
			return
		}
		go func(lookup regionLookup) {
			defer func() { <-permits }()
			// Report failures too: an unresolved node still completes its
			// lookup, so a provider that rejects quickly never waits out the
			// budget. The GeoIP client remembers the failure for later reads.
			result := regionLookupResult{index: lookup.index}
			if region, err := s.geoip.LookupCountry(lookupContext, lookup.address); err == nil {
				result.code = region.ISOCode
			}
			results <- result
		}(lookup)
	}

	budget := time.NewTimer(time.Until(deadline))
	defer budget.Stop()
	for remaining := len(lookups); remaining > 0; remaining-- {
		select {
		case result := <-results:
			if result.code != "" {
				agents[result.index].RegionCode = result.code
			}
		case <-budget.C:
			return
		}
	}
}

// acquireRegionPermit waits for a lookup slot until the shared budget expires.
func acquireRegionPermit(permits chan struct{}, deadline time.Time) bool {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case permits <- struct{}{}:
		return true
	case <-timer.C:
		return false
	}
}
