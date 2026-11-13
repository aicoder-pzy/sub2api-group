package service

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var codex780GatewayRE = regexp.MustCompile(`(?:^|[.])(?:chat\.)?gateway\.(unified-[0-9]{1,5})(?:[.]|$)`)

var codex780TargetRE = regexp.MustCompile(`^(?:chat\.gateway\.)?(unified-[0-9]{1,5})(?:\.api\.openai\.com)?$`)

var codex780GatewayAliasRE = regexp.MustCompile("^(?:unified[-_.]?)?([0-9]{1,5})$")

func normalizeCodex780Gateway(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "*" || target == "any" {
		return "any"
	}
	if match := codex780GatewayAliasRE.FindStringSubmatch(target); len(match) == 2 {
		return "unified-" + match[1]
	}
	if match := codex780TargetRE.FindStringSubmatch(target); len(match) == 2 {
		return match[1]
	}
	return target
}

func codex780GatewayAllowed(actual, target string) bool {
	return actual != "" && (target == "any" || actual == target)
}

// Only the two LB cookies are retained. JWT claims are routing hints, not verified identity.
func codex780Route(cookies []string, target string, now time.Time) ([]string, time.Time, error) {
	target = normalizeCodex780Gateway(target)
	bad := errors.New("route_cookie_invalid")
	pair := map[string]string{}
	for _, cookie := range cookies {
		name, value, ok := strings.Cut(cookie, "=")
		if name != "__cflb" && name != "__oailb" {
			continue
		}
		if !ok || value == "" || len(value) > 4096 || strings.ContainsAny(value, ";,\r\n\t ") || pair[name] != "" {
			return nil, time.Time{}, bad
		}
		for _, c := range value {
			if c < 33 || c > 126 {
				return nil, time.Time{}, bad
			}
		}
		pair[name] = value
	}
	if target == "" {
		return nil, time.Time{}, errors.New("route_target_missing")
	}
	if pair["__cflb"] == "" && pair["__oailb"] == "" {
		return nil, time.Time{}, errors.New("route_pair_missing")
	}
	if pair["__cflb"] == "" {
		return nil, time.Time{}, errors.New("route_cflb_missing")
	}
	if pair["__oailb"] == "" {
		return nil, time.Time{}, errors.New("route_oailb_missing")
	}
	parts := strings.Split(pair["__oailb"], ".")
	if len(parts) != 3 {
		return nil, time.Time{}, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, time.Time{}, bad
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp <= 0 || claims.Exp >= 4102444800 {
		return nil, time.Time{}, errors.New("route_expiry_invalid")
	}
	if claims.Exp <= now.Unix() {
		return nil, time.Time{}, errors.New("route_pair_expired")
	}
	match := codex780GatewayRE.FindSubmatch(raw)
	if len(match) != 2 {
		return nil, time.Time{}, errors.New("route_gateway_unknown")
	}
	if !codex780GatewayAllowed(string(match[1]), target) {
		return nil, time.Time{}, errors.New("route_gateway_mismatch: got=" + string(match[1]) + " want=" + target)
	}
	return []string{"__cflb=" + pair["__cflb"], "__oailb=" + pair["__oailb"]}, time.Unix(claims.Exp, 0), nil
}

func parseCodexTicketIssuedAt(value string, expected int, now time.Time) (time.Time, error) {
	if len(value) != expected || strings.ContainsAny(value, "\r\n\t ") {
		return time.Time{}, errors.New("ticket_length_mismatch")
	}
	core := strings.TrimRight(value, "=")
	raw, err := base64.RawURLEncoding.Strict().DecodeString(core)
	if len(value)-len(core) > 2 || err != nil || len(raw) < 73 || raw[0] != 0x80 || (len(raw)-57)%16 != 0 {
		return time.Time{}, errors.New("ticket_envelope_invalid")
	}
	issued := binary.BigEndian.Uint64(raw[1:9])
	if issued < 1577836800 || issued >= 4102444800 {
		return time.Time{}, errors.New("ticket_timestamp_invalid")
	}
	at := time.Unix(int64(issued), 0)
	if at.After(now.Add(30*time.Second)) || !now.Before(at.Add(240*time.Second)) {
		return time.Time{}, errors.New("ticket_expired")
	}
	return at, nil
}
