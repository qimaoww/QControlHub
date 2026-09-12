package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestUserManagementLoginRolesAndDisableWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	adminToken := "user-management-admin-token-0123456789"
	username := fmt.Sprintf("operator_%d", time.Now().UnixNano())
	password := "correct horse battery 123"
	server := New(dataStore, Config{AdminToken: adminToken})
	handler := server.Handler()

	createBody, _ := json.Marshal(core.UserRequest{Username: username, DisplayName: "运营人员", Role: core.RoleOperator, Password: password})
	create := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(createBody))
	create.Header.Set("Authorization", "Bearer "+adminToken)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var user core.User
	if err := json.NewDecoder(createResponse.Body).Decode(&user); err != nil {
		t.Fatalf("decode created user: %v", err)
	}
	if user.ID == "" || user.Username != username || user.Role != core.RoleOperator || user.Disabled {
		t.Fatalf("created user = %+v", user)
	}
	loginBody, _ := json.Marshal(map[string]string{"username": username, "token": password})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("user login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var session struct {
		Role     core.Role `json:"role"`
		UserID   string    `json:"user_id"`
		Username string    `json:"username"`
	}
	if err := json.NewDecoder(loginResponse.Body).Decode(&session); err != nil {
		t.Fatalf("decode user session: %v", err)
	}
	if session.Role != core.RoleOperator || session.UserID != user.ID || session.Username != username {
		t.Fatalf("user session = %+v", session)
	}
	cookie := loginResponse.Result().Cookies()[0]
	operatorRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	operatorRequest.AddCookie(cookie)
	operatorResponse := httptest.NewRecorder()
	handler.ServeHTTP(operatorResponse, operatorRequest)
	if operatorResponse.Code != http.StatusForbidden {
		t.Fatalf("operator list users status=%d body=%s", operatorResponse.Code, operatorResponse.Body.String())
	}

	delete := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+user.ID, nil)
	delete.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, delete)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("disable user status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	secondLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	secondLogin.Header.Set("Content-Type", "application/json")
	secondLoginResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondLoginResponse, secondLogin)
	if secondLoginResponse.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user login status=%d body=%s", secondLoginResponse.Code, secondLoginResponse.Body.String())
	}
}

// TestPurgeUserEndpointWithPostgreSQL covers the account-deletion route: an
// ordinary account cannot delete itself, an administrator can, a purged
// account can no longer log in, and an administrator account can only be
// disabled.
func TestPurgeUserEndpointWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	adminToken := "purge-endpoint-admin-token-0123456789"
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	suffix := time.Now().UnixNano()
	password := "correct horse battery 123"
	createAccount := func(request core.UserRequest) core.User {
		t.Helper()
		body, _ := json.Marshal(request)
		create := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(body))
		create.Header.Set("Authorization", "Bearer "+adminToken)
		create.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, create)
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s status=%d body=%s", request.Role, response.Code, response.Body.String())
		}
		var user core.User
		if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
			t.Fatalf("decode created user: %v", err)
		}
		return user
	}
	purge := func(id string, authorization string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+id+"/purge", nil)
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	user := createAccount(core.UserRequest{Username: fmt.Sprintf("purge_endpoint_%d", suffix), DisplayName: "待删除账号", Role: core.RoleUser, Password: password})
	loginBody, _ := json.Marshal(map[string]string{"username": user.Username, "token": password})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("user login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	if response := purge(user.ID, "", loginResponse.Result().Cookies()[0]); response.Code != http.StatusForbidden {
		t.Fatalf("ordinary account purged itself: status=%d body=%s", response.Code, response.Body.String())
	}

	if response := purge(user.ID, "Bearer "+adminToken, nil); response.Code != http.StatusNoContent {
		t.Fatalf("administrator purge status=%d body=%s", response.Code, response.Body.String())
	}
	if response := purge(user.ID, "Bearer "+adminToken, nil); response.Code != http.StatusNotFound {
		t.Fatalf("repeated purge status=%d body=%s", response.Code, response.Body.String())
	}
	relogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	relogin.Header.Set("Content-Type", "application/json")
	reloginResponse := httptest.NewRecorder()
	handler.ServeHTTP(reloginResponse, relogin)
	if reloginResponse.Code != http.StatusUnauthorized {
		t.Fatalf("purged account login status=%d body=%s", reloginResponse.Code, reloginResponse.Body.String())
	}

	administrator := createAccount(core.UserRequest{Username: fmt.Sprintf("purge_admin_%d", suffix), Role: core.RoleAdmin, Password: password})
	if response := purge(administrator.ID, "Bearer "+adminToken, nil); response.Code != http.StatusConflict {
		t.Fatalf("administrator purge status=%d body=%s", response.Code, response.Body.String())
	}
}
