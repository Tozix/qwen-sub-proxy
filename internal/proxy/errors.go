package proxy

import (
	"encoding/json"
	"net/http"
)

// errorEnvelope is the OpenAI-style error body used for proxy level failures.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError emits an OpenAI-compatible error envelope.
func writeError(w http.ResponseWriter, status int, errType, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{
		Message: message,
		Type:    errType,
		Code:    code,
	}})
}
