package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func ptr[T any](value T) *T { return &value }

func bearerToken(request *http.Request) string {
	value := request.Header.Get("Authorization")
	prefix, token, found := strings.Cut(value, " ")
	if !found || !strings.EqualFold(prefix, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func agentID(request *http.Request) string {
	return strings.TrimSpace(request.Header.Get(authn.HeaderAgentID))
}

func validAgentID(value string) bool {
	if len(value) != 20 || !strings.HasPrefix(value, "agt_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func constantTokenMatch(token string, expected [32]byte) bool {
	if token == "" {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}

func decodeJSON(w http.ResponseWriter, request *http.Request, destination any, limit int64) error {
	request.Body = http.MaxBytesReader(w, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	var portConflict *store.PortConflictError
	switch {
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &portConflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": chineseErrorMessage(http.StatusConflict, err.Error()),
			"code":  "port_conflict", "port": portConflict.Port, "field": portConflict.Field,
		})
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrSecretUnavailable):
		writeError(w, http.StatusServiceUnavailable, "protected credential storage is unavailable; configure QCH_CONFIG_ENCRYPTION_KEY")
	case errors.Is(err, store.ErrAuditUnavailable):
		writeError(w, http.StatusServiceUnavailable, "audit unavailable; enrollment credential was not disclosed")
	default:
		writeInternalError(w, err)
	}
}

func writeInternalError(w http.ResponseWriter, err error) {
	requestID, idErr := core.NewID("req")
	if idErr != nil {
		slog.Error("generate HTTP request ID", "error", idErr)
	}
	w.Header().Set("X-Request-ID", requestID)
	fields := []any{"request_id", requestID}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		// PgError.Detail can contain the entire failing row, including secrets.
		fields = append(fields, "sqlstate", pgError.Code, "constraint", pgError.ConstraintName,
			"table", pgError.TableName, "schema", pgError.SchemaName,
			// Preserve store/operation wrappers (e.g. account lookup) while
			// excluding the database message, which can quote input values.
			"error", strings.ReplaceAll(err.Error(), pgError.Error(), "PostgreSQL error"))
	} else {
		fields = append(fields, "error", err)
	}
	body := map[string]string{"error": chineseErrorMessage(http.StatusInternalServerError, "internal server error"), "request_id": requestID}
	var schemaError *store.TaskSchemaError
	if errors.As(err, &schemaError) {
		fields = append(fields, "stage", "task_creation", "agent_id", schemaError.AgentID, "action", schemaError.Action)
		body["code"] = "task_schema_incompatible"
		body["stage"] = "task_creation"
		body["error"] = "任务创建失败，尚未进入 Agent 执行。请联系管理员检查控制面升级及数据库迁移状态。"
	}
	slog.Error("HTTP request failed", fields...)
	writeJSON(w, http.StatusInternalServerError, body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": chineseErrorMessage(status, message)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}
