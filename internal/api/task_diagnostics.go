package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type failureResponseWriter struct {
	http.ResponseWriter
	server  *Server
	request *http.Request
}

func (w *failureResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *failureResponseWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }

func (w *failureResponseWriter) failure(status int, value any) any {
	if status < 400 {
		return value
	}
	b, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var body map[string]any
	if json.Unmarshal(b, &body) != nil {
		return value
	}
	requestID, _ := body["request_id"].(string)
	if requestID == "" {
		requestID, _ = core.NewID("req")
		body["request_id"] = requestID
	}
	w.Header().Set("X-Request-ID", requestID)
	message, _ := body["error"].(string)
	detail, _ := body["detail"].(string)
	if detail == "" {
		detail = message
	}
	code, _ := body["code"].(string)
	field, _ := body["field"].(string)
	stage, _ := body["stage"].(string)
	if stage == "" {
		stage = "api_request"
		if readOnlyMethod(w.request.Method) {
			stage = "data_read"
		}
	}
	// Reading a page is not an execution attempt. Keep the HTTP failure and
	// its diagnostic ID, but never turn polling or navigation into failed tasks.
	// This policy applies equally to sessions and API Key responses.
	if readOnlyMethod(w.request.Method) || w.request.URL.Path == "/api/v1/tasks/failures" {
		body["failure_retention"] = "not_required"
		body["stage"] = stage
		// Internal errors already log the same request ID in writeInternalError.
		// Preserve gateway/deadline diagnostics that writeError otherwise omits.
		if readOnlyMethod(w.request.Method) && status > 500 && code != "read_timeout" {
			slog.Error("HTTP read failed", "request_id", requestID, "method", w.request.Method, "route", w.request.Pattern, "resource", w.request.URL.EscapedPath(), "stage", stage, "status", status, "error", core.RedactDiagnostic(detail))
		}
		return body
	}
	body["failure_retention"] = "failed"
	if w.server.store == nil {
		body["failure_saved"] = false
		body["retention_error"] = "未能保存到任务：任务存储不可用。"
		return body
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(w.request.Context()), 3*time.Second)
	defer cancel()
	var target core.PanelFailure
	if id := w.request.PathValue("id"); validAgentID(id) {
		if _, err := w.server.store.GetAgent(ctx, id); err == nil {
			target.AgentID = id
			engine := core.Engine(w.request.PathValue("engine"))
			if engine.Valid() {
				target.Engine = engine
			}
		}
	}
	id, saveErr := w.server.store.RecordPanelFailure(ctx, core.PanelFailure{
		Operation: w.request.Pattern, Resource: w.request.URL.EscapedPath(), AgentID: target.AgentID, Engine: target.Engine, Stage: stage, Summary: message, Detail: detail, RequestID: requestID, HTTPStatus: status, Code: code, Field: field,
	})
	if errors.Is(saveErr, store.ErrForbidden) || errors.Is(saveErr, store.ErrNotFound) {
		id, saveErr = w.server.store.RecordPanelFailure(ctx, core.PanelFailure{Operation: w.request.Pattern, Resource: w.request.URL.EscapedPath(), Stage: stage, Summary: message, Detail: detail, RequestID: requestID, HTTPStatus: status, Code: code, Field: field})
	}
	if saveErr != nil {
		body["failure_saved"] = false
		body["retention_error"] = "未能保存到任务：" + core.RedactDiagnostic(safeInternalDiagnostic(saveErr))
	} else {
		body["failure_retention"] = "saved"
		body["failure_saved"] = true
		body["failure_task_id"] = id
	}
	return body
}

func readOnlyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func (s *Server) recordPanelFailure(w http.ResponseWriter, r *http.Request) {
	var input core.PanelFailure
	if err := decodeJSON(w, r, &input, core.MaxEncodedTaskDiagnosticBytes); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// Older frontends may backfill a read failure despite the response policy.
	// Decide at the API boundary too, before touching diagnostic storage.
	method, _, _ := strings.Cut(strings.TrimSpace(input.Operation), " ")
	if readOnlyMethod(strings.ToUpper(method)) {
		writeJSON(w, http.StatusOK, map[string]string{"failure_retention": "not_required"})
		return
	}
	id, err := s.store.RecordPanelFailure(r.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}

func (s *Server) getTaskDiagnostic(w http.ResponseWriter, r *http.Request) {
	var selected []int
	if value := r.URL.Query().Get("attempt"); value != "" {
		attempt, err := strconv.Atoi(value)
		if err != nil || attempt < 0 {
			writeStoreError(w, store.ErrInvalid)
			return
		}
		selected = []int{attempt}
	}
	d, err := s.store.TaskDiagnostic(r.Context(), r.PathValue("id"), selected...)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) uploadTaskDiagnostic(w http.ResponseWriter, r *http.Request) {
	var chunk core.TaskDiagnosticChunk
	if err := decodeJSON(w, r, &chunk, 256<<10); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.UploadTaskDiagnostic(r.Context(), agentID(r), r.PathValue("id"), chunk); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
