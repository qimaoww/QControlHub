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
	indexes []int
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
	byAddress := make(map[netip.Addr]int, len(agents))
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
		address = address.Unmap()
		if lookupIndex, ok := byAddress[address]; ok {
			lookups[lookupIndex].indexes = append(lookups[lookupIndex].indexes, index)
			continue
		}
		byAddress[address] = len(lookups)
		lookups = append(lookups, regionLookup{indexes: []int{index}, address: address})
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
	jobs := make(chan int, len(lookups))
	for index := range lookups {
		jobs <- index
	}
	close(jobs)
	for range min(regionResolveConcurrency, len(lookups)) {
		go func() {
			for index := range jobs {
				// Finish an already-started lookup into the cache, but do not
				// start queued work after this list's budget has expired.
				if !time.Now().Before(deadline) {
					return
				}
				// Report failures too, so a provider that rejects quickly
				// never makes the list wait out the whole budget.
				result := regionLookupResult{index: index}
				if region, err := s.geoip.LookupCountry(lookupContext, lookups[index].address); err == nil {
					result.code = region.ISOCode
				}
				results <- result
			}
		}()
	}
	applyResult := func(result regionLookupResult) {
		if result.code == "" {
			return
		}
		for _, index := range lookups[result.index].indexes {
			agents[index].RegionCode = result.code
		}
	}

	budget := time.NewTimer(time.Until(deadline))
	defer budget.Stop()
	for remaining := len(lookups); remaining > 0; remaining-- {
		select {
		case result := <-results:
			applyResult(result)
		case <-budget.C:
			// The timer and a completed result can become ready together.
			// Preserve all results already available when the budget ends.
			for {
				select {
				case result := <-results:
					applyResult(result)
				default:
					return
				}
			}
		}
	}
}
