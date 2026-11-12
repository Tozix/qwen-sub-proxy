package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// streamBody copies an upstream SSE body to the client and flushes after
// every chunk so the client sees tokens as they are produced. An abnormal end
// of the upstream stream is reported as an SSE error event, otherwise the
// client could not tell a truncated stream from a completed one.
func streamBody(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				writeStreamError(w, flusher, err)
			}
			return
		}
	}
}

// writeStreamError emits a final SSE error event.
func writeStreamError(w http.ResponseWriter, flusher http.Flusher, err error) {
	payload, marshalErr := json.Marshal(errorEnvelope{Error: errorBody{
		Message: "upstream stream ended unexpectedly: " + err.Error(),
		Type:    "upstream_error",
		Code:    "stream_interrupted",
	}})
	if marshalErr != nil {
		return
	}
	_, _ = w.Write([]byte("data: " + string(payload) + "\n\n"))
	if flusher != nil {
		flusher.Flush()
	}
}
