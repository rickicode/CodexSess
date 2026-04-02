package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleZoModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	key, rawKey, err := s.resolveZoKeyForRequest(r.Context())
	if err != nil {
		respondErr(w, 503, "zo_api_key_missing", err.Error())
		return
	}
	_, _ = s.svc.IncrementZoAPIKeyUsage(r.Context(), key.ID, 1)
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.zo.computer/models/available", nil)
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rawKey))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		respondErr(w, 502, "upstream_error", err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respondErr(w, 502, "upstream_error", fmt.Sprintf("zo api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	var parsed struct {
		Models []struct {
			ModelName string `json:"model_name"`
			Vendor    string `json:"vendor"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		respondErr(w, 502, "upstream_error", "failed to parse zo models")
		return
	}
	out := ModelsResponse{
		Object: "list",
		Data:   make([]ModelInfo, 0, len(parsed.Models)),
	}
	for _, item := range parsed.Models {
		id := strings.TrimSpace(item.ModelName)
		if id == "" {
			continue
		}
		out.Data = append(out.Data, ModelInfo{
			ID:      id,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: strings.TrimSpace(item.Vendor),
		})
	}
	respondJSON(w, 200, out)
}
