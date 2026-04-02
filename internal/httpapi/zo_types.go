package httpapi

import (
	"encoding/json"
	"time"
)

const zoAskEndpoint = "https://api.zo.computer/zo/ask"
const zoConversationTTL = 30 * time.Minute

type zoAskRequest struct {
	Input          string          `json:"input"`
	ModelName      string          `json:"model_name,omitempty"`
	PersonaID      string          `json:"persona_id,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	OutputFormat   json.RawMessage `json:"output_format,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
}

type zoAskResponse struct {
	Output         any    `json:"output"`
	Response       any    `json:"response"`
	Text           any    `json:"text"`
	ConversationID string `json:"conversation_id"`
}

type zoAskResult struct {
	Text           string
	ConversationID string
}
