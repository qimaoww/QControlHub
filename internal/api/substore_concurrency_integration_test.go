package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestSubStoreMutationsShareDatabaseLock(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	release, err := db.TryLockSubStoreOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// A separate API instance must honor the same database-scoped lock.
	const token = "substore-concurrency-administrator"
	other := configScopeAPIClient{t: t, handler: New(db, Config{AdminToken: token}).Handler(), token: token}
	for _, operation := range []struct{ method, path string }{
		{"PUT", "/substore-sync/settings"},
		{"POST", "/substore-sync/targets"},
		{"POST", "/substore-sync/targets/import"},
		{"POST", "/substore-sync/targets/example/remote"},
		{"PUT", "/substore-sync/targets/example"},
		{"DELETE", "/substore-sync/targets/example"},
		{"PUT", "/substore-sync/selections"},
		{"POST", "/substore-sync/run"},
	} {
		other.call(operation.method, operation.path, nil, http.StatusConflict, nil)
	}
	alice.call("GET", "/substore-sync", nil, http.StatusOK, nil)
	release()
	// Both an unsuccessful handler and a successful handler release the lock.
	other.call("POST", "/substore-sync/targets", map[string]string{"display_name": ""}, http.StatusBadRequest, nil)
	other.call("POST", "/substore-sync/targets", map[string]string{"display_name": "after-failure"}, http.StatusCreated, nil)
}

func TestSubStoreConcurrentRelinkCannotOverwriteAnotherUsersClaim(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	var first, second core.SubStoreSyncTarget
	alice.call("POST", "/substore-sync/targets", map[string]string{"display_name": "alice"}, http.StatusCreated, &first)
	bob.call("POST", "/substore-sync/targets", map[string]string{"display_name": "bob"}, http.StatusCreated, &second)

	entered, unblock := make(chan struct{}), make(chan struct{})
	var closeOnce sync.Once
	release := func() { closeOnce.Do(func() { close(unblock) }) }
	defer release()
	var patches atomic.Int32
	var remoteMu sync.Mutex
	remote := map[string]any{"name": "unclaimed", "content": ""}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/secret/api/subs":
			remoteMu.Lock()
			snapshot := cloneSubStoreSubscription(remote)
			remoteMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": []any{snapshot}})
		case r.Method == http.MethodPatch && r.URL.Path == "/secret/api/sub/unclaimed":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if patches.Add(1) == 1 {
				close(entered)
				select {
				case <-unblock:
				case <-r.Context().Done():
					return
				}
			}
			remoteMu.Lock()
			remote = payload
			remoteMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": payload})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	// Release the blocked HTTP request before closing its test server.
	defer release()
	alice.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: upstream.URL + "/secret"}, http.StatusOK, nil)
	bob.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: upstream.URL + "/secret"}, http.StatusOK, nil)
	body, _ := json.Marshal(map[string]string{"subscription_name": "unclaimed", "sync_format": "mihomo"})
	request := httptest.NewRequest("POST", "/api/v1/substore-sync/targets/"+first.ID+"/remote", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(alice.cookie)
	request.Header.Set(csrfHeader, alice.csrf)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		alice.handler.ServeHTTP(response, request)
		completed <- response
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first relink did not reach the remote claim")
	}
	bob.call("POST", "/substore-sync/targets/"+second.ID+"/remote", map[string]string{"subscription_name": "unclaimed"}, http.StatusConflict, nil)
	release()
	select {
	case response := <-completed:
		if response.Code != http.StatusOK {
			t.Fatalf("first relink: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first relink did not finish")
	}
	// Once the lock is released the persistent cross-user claim still denies
	// the second request, without a remote PATCH or rollback.
	bob.call("POST", "/substore-sync/targets/"+second.ID+"/remote", map[string]string{"subscription_name": "unclaimed"}, http.StatusConflict, nil)
	remoteMu.Lock()
	owner := remote["qcontrolhub_integration_id"]
	remoteMu.Unlock()
	alice.call("GET", "/substore-sync?target_id="+first.ID, nil, http.StatusOK, nil)
	saved, err := db.SubStoreSyncTarget(store.WithConfigScope(ctx, alice.userID, false), first.ID)
	if err != nil || saved.SubscriptionName != "unclaimed" || saved.SyncFormat != core.SubStoreSyncFormatMihomo {
		t.Fatalf("winning relink was not persisted: %+v %v", saved, err)
	}
	if owner != saved.IntegrationID || patches.Load() != 1 {
		t.Fatalf("competing relink rewrote the remote owner: %v, patches=%d", owner, patches.Load())
	}
}

func TestSubStoreIndependentBackendsDoNotBlockAPIWrites(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	alice.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: "https://substore.example/alice"}, http.StatusOK, nil)
	bob.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: "https://substore.example/bob"}, http.StatusOK, nil)
	release, err := db.TryLockSubStoreOperation(store.WithConfigScope(ctx, alice.userID, false))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	alice.call("POST", "/substore-sync/targets", map[string]string{"display_name": "same-name"}, http.StatusConflict, nil)
	bob.call("POST", "/substore-sync/targets", map[string]string{"display_name": "same-name"}, http.StatusCreated, nil)
	// Changing to the other user's busy backend also takes its canonical lock.
	bob.call("PUT", "/substore-sync/settings", subStoreSettingsRequest{EndpointURL: "https://SUBSTORE.example:443/alice/"}, http.StatusConflict, nil)
}
