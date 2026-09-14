package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) enrollmentDownloadAllowed(w http.ResponseWriter, request *http.Request) bool {
	token := strings.TrimSpace(request.Header.Get("X-QControlHub-Enrollment"))
	if s.store == nil || !s.store.EnrollmentTokenUsable(request.Context(), token) {
		writeError(w, http.StatusUnauthorized, "valid add-node credential required")
		return false
	}
	return true
}

func acceptsGzip(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Accept-Encoding")), "gzip")
}

func gzipCompress(input []byte) []byte {
	if len(input) == 0 {
		return nil
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = writer.Write(input)
	_ = writer.Close()
	return buffer.Bytes()
}

// writeAgentBinary writes the immutable agent executable. When the client
// advertises gzip support it is sent with Content-Encoding: gzip, which
// conforming clients (Go's transport and curl --compressed) decompress before
// hashing or saving it. The checksum header always reflects the raw binary.
func (s *Server) writeAgentBinary(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	body := s.agentBinary
	if len(s.agentBinaryGzip) != 0 && acceptsGzip(request) {
		body = s.agentBinaryGzip
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// agentBinary serves the statically-extracted agent executable only to a valid
// node-bound add-node credential.
func (s *Server) serveAgentBinary(w http.ResponseWriter, r *http.Request) {
	if len(s.agentBinary) == 0 {
		http.NotFound(w, r)
		return
	}
	if !s.enrollmentDownloadAllowed(w, r) {
		return
	}
	s.writeAgentBinary(w, r)
}

// serveAgentBinaryForAgent serves the same immutable binary as the installer,
// but authenticates an already-enrolled Agent with its Ed25519 request
// signature. This keeps the enrollment credential out of long-lived Agent
// state while allowing an operator to upgrade it from the panel.
func (s *Server) serveAgentBinaryForAgent(w http.ResponseWriter, r *http.Request) {
	if len(s.agentBinary) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-QControlHub-Agent-SHA256", fmt.Sprintf("%x", sha256.Sum256(s.agentBinary)))
	if s.agentVersion != "" {
		w.Header().Set("X-QControlHub-Agent-Version", s.agentVersion)
	}
	s.writeAgentBinary(w, r)
}

func (s *Server) serveAgentInstaller(w http.ResponseWriter, r *http.Request) {
	if len(s.agentInstaller) == 0 {
		http.NotFound(w, r)
		return
	}
	if !s.enrollmentDownloadAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="install-agent.sh"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.agentInstaller)
}
