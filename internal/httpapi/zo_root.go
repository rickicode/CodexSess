package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) handleZoV1Root(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleZoModels(w, r)
		return
	case http.MethodPost:
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON body")
			return
		}
		if _, ok := anyBody["messages"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleZoChatCompletions(w, r)
			return
		}
		respondErr(w, 400, "bad_request", "unsupported /zo/v1 payload, use /zo/v1/chat/completions")
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleZoNotSupported(w http.ResponseWriter, r *http.Request) {
	respondErr(w, http.StatusNotFound, "not_supported", "Zo proxy only supports /zo/v1/chat/completions")
}
