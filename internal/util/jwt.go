package util

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Claims struct {
	Email     string
	PlanType  string
	AccountID string
	OrgID     string
}

func AccessTokenExpiry(accessToken string) (time.Time, error) {
	payload, err := decodeJWTPayload(accessToken)
	if err != nil {
		return time.Time{}, err
	}
	exp, ok := payload["exp"].(float64)
	if !ok || exp <= 0 {
		return time.Time{}, fmt.Errorf("exp not found in access token")
	}
	return time.Unix(int64(exp), 0).UTC(), nil
}

func ParseClaims(idToken, accessToken string) (Claims, error) {
	idPayload, err := decodeJWTPayload(idToken)
	if err != nil {
		return Claims{}, err
	}
	accessPayload, _ := decodeJWTPayload(accessToken)

	claims := Claims{}
	if v, ok := idPayload["email"].(string); ok {
		claims.Email = strings.TrimSpace(v)
	}
	if auth, ok := idPayload["https://api.openai.com/auth"].(map[string]any); ok {
		if v, ok := auth["chatgpt_plan_type"].(string); ok {
			claims.PlanType = v
		}
		if v, ok := auth["account_id"].(string); ok {
			claims.AccountID = v
		}
		if v, ok := auth["organization_id"].(string); ok {
			claims.OrgID = v
		}
	}
	if auth, ok := accessPayload["https://api.openai.com/auth"].(map[string]any); ok {
		if claims.AccountID == "" {
			if v, ok := auth["chatgpt_account_id"].(string); ok {
				claims.AccountID = v
			}
		}
		if claims.OrgID == "" {
			if v, ok := auth["organization_id"].(string); ok {
				claims.OrgID = v
			}
		}
	}
	if claims.Email == "" {
		return Claims{}, fmt.Errorf("email not found in token")
	}
	return claims, nil
}

func decodeJWTPayload(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
