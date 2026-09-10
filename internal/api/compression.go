package api

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Core log windows and other list endpoints produce multi-megabyte JSON
// responses whose content is highly repetitive, while the panel is polled on
// a short interval. Compressing them cuts a full 8000-entry log window from
// ~2.5 MiB to under 60 KiB on the wire.
//
// Kernel log lines are repetitive enough that a mid-low level reaches within a
// few percent of the default ratio while costing a fraction of the CPU, which
// matters because every open panel polls these responses.
const (
	compressMinBytes = 512
	compressLevel    = 3
)

var gzipWriterPool = sync.Pool{
	New: func() any {
		writer, err := gzip.NewWriterLevel(nil, compressLevel)
		if err != nil {
			panic(err)
		}
		return writer
	},
}

// mustCompressResponse reports whether a response body of this status and
// header set may be gzip encoded. The response must not already carry a
// content encoding: re-compressing an encoded body would inflate it.
func mustCompressResponse(status int, header http.Header) bool {
	if status < http.StatusOK || status == http.StatusNoContent || status == http.StatusNotModified {
		return false
	}
	if header.Get("Content-Encoding") != "" {
		return false
	}
	if header.Get("Upgrade") != "" {
		// A protocol upgrade (agent websocket) owns the connection after the
		// handshake and must not be wrapped in a buffered body.
		return false
	}
	return true
}

// compressedResponseWriter buffers a response until it either exceeds the
// compression threshold or the handler finishes empty-handed.
type compressedResponseWriter struct {
	http.ResponseWriter
	gzip        *gzip.Writer
	buffer      []byte
	status      int
	wroteHeader bool
	compressing bool
	finished    bool
}

// Unwrap lets http.ResponseController reach the underlying writer so that
// protocol upgrades and connection control keep working through the wrapper.
func (writer *compressedResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *compressedResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.status = status
	writer.wroteHeader = true
	// The status stays uncommitted until the body size is known: a response
	// below the threshold must keep its original Content-Length and framing.
	if !mustCompressResponse(status, writer.Header()) {
		writer.ResponseWriter.WriteHeader(status)
	}
}

// Write keeps small responses on the uncompressed path and hands everything
// above the threshold to the gzip stream.
func (writer *compressedResponseWriter) Write(payload []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	if !mustCompressResponse(writer.status, writer.Header()) {
		return writer.ResponseWriter.Write(payload)
	}
	if writer.compressing {
		if _, err := writer.gzip.Write(payload); err != nil {
			return 0, err
		}
		return len(payload), nil
	}
	// The buffer only ever accumulates sub-threshold writes, so it is bounded
	// by compressMinBytes and never grows with the response size.
	if len(writer.buffer)+len(payload) >= compressMinBytes {
		return writer.startCompression(payload)
	}
	writer.buffer = append(writer.buffer, payload...)
	return len(payload), nil
}

// startCompression begins the gzip stream, flushes the buffered prefix through
// it, and writes the payload that crossed the threshold.
func (writer *compressedResponseWriter) startCompression(payload []byte) (int, error) {
	header := writer.Header()
	header.Set("Content-Encoding", "gzip")
	// The body length changes under gzip, and the negotiated encoding is part
	// of the response, so caches and proxies must key on Accept-Encoding.
	header.Del("Content-Length")
	header.Add("Vary", "Accept-Encoding")
	compressor := gzipWriterPool.Get().(*gzip.Writer)
	writer.gzip = compressor
	writer.compressing = true
	// Commit the status before the compressor writes the gzip header, so the
	// handler's status code is not replaced by an implicit 200.
	writer.ResponseWriter.WriteHeader(writer.status)
	compressor.Reset(writer.ResponseWriter)
	if len(writer.buffer) > 0 {
		if _, err := compressor.Write(writer.buffer); err != nil {
			return 0, err
		}
		writer.buffer = writer.buffer[:0]
	}
	if _, err := compressor.Write(payload); err != nil {
		return 0, err
	}
	// Report the caller's payload length even though the wire size differs.
	return len(payload), nil
}

// finish closes the gzip stream, or flushes the buffered body unchanged when
// the response stayed below the compression threshold.
func (writer *compressedResponseWriter) finish() {
	if writer.finished {
		return
	}
	writer.finished = true
	if writer.compressing {
		_ = writer.gzip.Close()
		gzipWriterPool.Put(writer.gzip)
		writer.gzip = nil
		writer.compressing = false
		return
	}
	if !writer.wroteHeader {
		writer.ResponseWriter.WriteHeader(http.StatusOK)
	} else if len(writer.buffer) > 0 {
		// The status was deferred until the body size was known; commit the
		// handler's status before flushing the buffered body unchanged.
		writer.ResponseWriter.WriteHeader(writer.status)
	}
	if len(writer.buffer) == 0 {
		return
	}
	_, _ = writer.ResponseWriter.Write(writer.buffer)
	writer.buffer = nil
}

func requestAcceptsGzip(request *http.Request) bool {
	for _, value := range request.Header.Values("Accept-Encoding") {
		for _, token := range strings.Split(value, ",") {
			name, parameters, _ := strings.Cut(strings.TrimSpace(token), ";")
			if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
				continue
			}
			// "gzip;q=0" explicitly refuses gzip.
			if quality := parseEncodingQuality(parameters); quality == 0 {
				return false
			}
			return true
		}
	}
	return false
}

func parseEncodingQuality(parameters string) float64 {
	for _, parameter := range strings.Split(parameters, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0
		}
		return quality
	}
	return 1
}
