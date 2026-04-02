package service

import (
	"errors"

	"github.com/ricki/codexsess/internal/store"
)

const (
	codingEventPersistMax      = 240
	codingEventContentMaxRunes = 6000
)

var ErrCodingSessionBusy = errors.New("coding session is already processing")

type CodingChatResult struct {
	Session           store.CodingSession
	User              store.CodingMessage
	Assistant         store.CodingMessage
	Assistants        []store.CodingMessage
	EventMessages     []store.CodingMessage
	CachedInputTokens int
}
