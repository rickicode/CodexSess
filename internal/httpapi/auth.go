package httpapi

import (
	"strings"
)

func ResolveAccountHeader(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	return v, nil
}

func BearerToken(raw string) string {
	parts := strings.SplitN(strings.TrimSpace(raw), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
