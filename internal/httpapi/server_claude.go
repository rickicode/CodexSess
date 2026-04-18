package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

func (s *Server) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	reqID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	state, ok := s.decodeClaudeMessagesRequest(w, r, reqID)
	if !ok {
		return
	}
	s.executeProxyProtocol(w, r, proxyPipeline{
		Plan: proxyProtocolPlan{
			RequestID:  reqID,
			Selector:   "",
			Model:      state.request.Model,
			Prompt:     state.prompt,
			DirectOpts: state.directOpts,
			Stream:     state.request.Stream,
		},
		Adapter: proxyProtocolAdapter{
			WriteSetupError: func(w http.ResponseWriter, err error) {
				writeClaudeExecutionSetupError(w, err, reqID)
			},
			WriteStream: func(w http.ResponseWriter, r *http.Request, exec *proxyExecutionSession, status *int) {
				s.writeClaudeMessagesStreamResponse(w, r, reqID, state, exec, status)
			},
			WriteJSON: func(w http.ResponseWriter, result proxyBackendResult, _ *int) {
				writeClaudeMessagesJSONResponse(w, reqID, state, result)
			},
			WriteError: func(w http.ResponseWriter, err error, status *int) {
				code, errType := claudeWireTranslator.ClassifyUpstreamError(err)
				*status = code
				respondClaudeErr(w, code, errType, err.Error(), reqID)
			},
		},
	})
}
