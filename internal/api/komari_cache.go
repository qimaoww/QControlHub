package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Komari traffic comes from an external provider and one read costs about a
// second. A node grid used to ask once per card and every ask reached the
// provider again, so each render paid that cost. This cache keeps the last
// resource per node, lets the node list inline it without waiting for the
// provider, and refreshes a stale entry in the background instead of making the
// reader wait.
const (
	komariNodeCacheTTL  = 60 * time.Second
	komariRefreshWindow = 15 * time.Second
)

type cachedKomariNode struct {
	uuid       string
	node       core.KomariNode
	expiresAt  time.Time
	refreshing bool
}

// peekKomariNode returns a cached resource, including a stale copy, without
// reading through to the provider.
func (s *Server) peekKomariNode(agentID, uuid string) (core.KomariNode, bool) {
	s.komariCacheMu.Lock()
	defer s.komariCacheMu.Unlock()
	entry, ok := s.komariCache[agentID]
	if !ok || entry.uuid != uuid {
		return core.KomariNode{}, false
	}
	return entry.node, true
}

// freshKomariNode returns a cached resource only while it is inside its TTL.
func (s *Server) freshKomariNode(agentID, uuid string, now time.Time) (core.KomariNode, bool) {
	s.komariCacheMu.Lock()
	defer s.komariCacheMu.Unlock()
	entry, ok := s.komariCache[agentID]
	if !ok || entry.uuid != uuid || !now.Before(entry.expiresAt) {
		return core.KomariNode{}, false
	}
	return entry.node, true
}

// storeKomariNode caches one provider answer and clears the refresh claim.
func (s *Server) storeKomariNode(agentID, uuid string, node core.KomariNode, now time.Time) {
	s.komariCacheMu.Lock()
	defer s.komariCacheMu.Unlock()
	if s.komariCache == nil {
		s.komariCache = make(map[string]cachedKomariNode)
	}
	s.komariCache[agentID] = cachedKomariNode{
		uuid:      uuid,
		node:      node,
		expiresAt: now.Add(komariNodeCacheTTL),
	}
}

// forgetKomariNode drops a cached resource, for example after the binding that
// produced it changed or disappeared.
func (s *Server) forgetKomariNode(agentID string) {
	s.komariCacheMu.Lock()
	defer s.komariCacheMu.Unlock()
	delete(s.komariCache, agentID)
}

// claimKomariRefresh reports whether this caller should read the provider, and
// claims the entry so concurrent readers do not all call it at once.
func (s *Server) claimKomariRefresh(agentID, uuid string, now time.Time) bool {
	s.komariCacheMu.Lock()
	defer s.komariCacheMu.Unlock()
	entry, ok := s.komariCache[agentID]
	if ok && entry.uuid == uuid {
		if entry.refreshing || now.Before(entry.expiresAt) {
			return false
		}
	}
	entry.uuid = uuid
	entry.refreshing = true
	if s.komariCache == nil {
		s.komariCache = make(map[string]cachedKomariNode)
	}
	s.komariCache[agentID] = entry
	return true
}

// releaseKomariRefresh gives up a claim after a failed read so the next render
// can retry.
func (s *Server) releaseKomariRefresh(agentID, uuid string) {
	s.komariCacheMu.Lock()
	defer s.komariCacheMu.Unlock()
	entry, ok := s.komariCache[agentID]
	if ok && entry.uuid == uuid && entry.refreshing {
		entry.refreshing = false
		s.komariCache[agentID] = entry
	}
}

// fetchKomariNode reads one node from the configured provider.
func (s *Server) fetchKomariNode(ctx context.Context, uuid string, allowEnvironment bool) (core.KomariNode, error) {
	client, err := s.komariForRequest(ctx, allowEnvironment)
	if err != nil {
		return core.KomariNode{}, err
	}
	if client == nil {
		return core.KomariNode{}, errors.New("Komari integration is not configured")
	}
	node, err := client.GetNode(ctx, uuid)
	if err != nil {
		return core.KomariNode{}, err
	}
	return komariNodeResource(node), nil
}

// refreshKomariNodeAsync renews a stale entry in the background. The node list
// keeps its current response instead of waiting for the provider, which is what
// keeps a slow or unreachable Komari from slowing the panel down.
func (s *Server) refreshKomariNodeAsync(agentID, uuid string, allowEnvironment bool) {
	if !s.claimKomariRefresh(agentID, uuid, time.Now()) {
		return
	}
	go func() {
		defer s.releaseKomariRefresh(agentID, uuid)
		ctx, cancel := context.WithTimeout(context.Background(), komariRefreshWindow)
		defer cancel()
		node, err := s.fetchKomariNode(ctx, uuid, allowEnvironment)
		if err != nil {
			slog.Debug("komari refresh failed", "agent", agentID, "error", err)
			return
		}
		s.storeKomariNode(agentID, uuid, node, time.Now())
	}()
}

// attachAgentKomari inlines the cached resource for every bound node so a node
// grid renders monthly traffic without one request per card. It never waits for
// the provider: a node without a cached answer keeps the per-node endpoint, and
// a stale answer triggers the background refresh above.
func (s *Server) attachAgentKomari(request *http.Request, agents []core.Agent) {
	allowEnvironment := s.configOwnerID(request) == ""
	for index := range agents {
		uuid := store.AgentKomariUUID(agents[index])
		if uuid == "" {
			continue
		}
		node, ok := s.peekKomariNode(agents[index].ID, uuid)
		if !ok {
			continue
		}
		cached := node
		agents[index].Komari = &cached
		s.refreshKomariNodeAsync(agents[index].ID, uuid, allowEnvironment)
	}
}
