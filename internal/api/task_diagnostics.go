package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	if status < 400 || w.request.URL.Path == "/api/v1/tasks/failures" {
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
	}
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
		body["failure_saved"] = true
		body["failure_task_id"] = id
	}
	return body
}

func (s *Server) recordPanelFailure(w http.ResponseWriter, r *http.Request) {
	var input core.PanelFailure
	if err := decodeJSON(w, r, &input, core.MaxEncodedTaskDiagnosticBytes); err != nil {
		writeError(w, 400, err.Error())
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
