package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// Adapted from ranxi2001/sub2api PRs #500 and #545. Only structured error
// fields are trusted: echoed prompts and unrelated 403s are not exemptions.
func isOpenAIUpstreamSessionBlocked(body []byte) bool {
	return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "error.code").String()), "session_blocked_by_cyber_policy")
}

func isOpenAIRequestScoped403(account *Account, statusCode int, body []byte) bool {
	if account == nil || account.Platform != PlatformOpenAI || statusCode != http.StatusForbidden {
		return false
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(payload.Error.Code))
	errorType := strings.ToLower(strings.TrimSpace(payload.Error.Type))
	if code == "content_policy_violation" || (code == "" && errorType == "content_policy_violation") {
		return true
	}
	return errorType == "upstream_error" && (code == "" || code == "upstream_error") &&
		strings.EqualFold(strings.TrimSpace(payload.Error.Message), "failed to WebSocket dial: expected handshake response status code 101 but got 403")
}

func isOpenAIRelayRequestError(account *Account, statusCode int, body []byte) bool {
	return account != nil && account.IsOpenAICompatible() &&
		(isOpenAIUpstreamSessionBlocked(body) || isOpenAIRequestScoped403(account, statusCode, body))
}

func isOpenAIRelayPolicyRejection(account *Account, statusCode int, body []byte) bool {
	if account == nil || !account.IsOpenAICompatible() {
		return false
	}
	if isOpenAIUpstreamSessionBlocked(body) {
		return true
	}
	code := strings.TrimSpace(gjson.GetBytes(body, "error.code").String())
	errorType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	return statusCode == http.StatusForbidden && (strings.EqualFold(code, "content_policy_violation") ||
		(code == "" && strings.EqualFold(errorType, "content_policy_violation")))
}
