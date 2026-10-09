package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var balanceNotificationHTTPClient = newSSRFSafeHTTPClient(10 * time.Second)

func validateBalanceNotificationURL(provider, raw string) error {
	invalid := errors.New("invalid public HTTPS notification URL")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n#") || u.Opaque != "" {
		return invalid
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if isBlockedHostname(host) || strings.HasSuffix(host, ".localhost") || strings.ContainsAny(host, "%\\") {
		return invalid
	}
	if ip := net.ParseIP(host); ip != nil && (isPrivateIP(ip) || !ip.IsGlobalUnicast()) {
		return invalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid
	}
	if provider == "webhook" {
		if host == "qyapi.weixin.qq.com" || host == "oapi.dingtalk.com" || host == "open.feishu.cn" {
			return invalid
		}
		if port := u.Port(); port != "" {
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				return invalid
			}
		}
		if strings.HasSuffix(u.Host, ":") {
			return invalid
		}
		return nil
	}
	if u.Port() != "" || u.RawPath != "" {
		return invalid
	}
	switch provider {
	case "wecom":
		if host == "qyapi.weixin.qq.com" && u.Path == "/cgi-bin/webhook/send" && len(query) == 1 && len(query["key"]) == 1 && query.Get("key") != "" {
			return nil
		}
	case "dingtalk":
		if host == "oapi.dingtalk.com" && u.Path == "/robot/send" && len(query) == 1 && len(query["access_token"]) == 1 && query.Get("access_token") != "" {
			return nil
		}
	case "feishu":
		suffix := strings.TrimPrefix(u.Path, "/open-apis/bot/v2/hook/")
		if host == "open.feishu.cn" && suffix != u.Path && suffix != "" && !strings.Contains(suffix, "/") && len(query) == 0 {
			return nil
		}
	}
	return invalid
}

func signBalanceNotification(key, message string) string {
	h := hmac.New(sha256.New, []byte(key))
	_, _ = h.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (s *UpstreamBillingProbeService) sendBalanceNotification(ctx context.Context, channel UpstreamBalanceNotificationChannel, event *UpstreamBalanceNotificationEvent) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	subject := balanceNotificationSubject(event)
	text := fmt.Sprintf("%s\nScope: %s\nBalance: %g %s\nThreshold: %g %s\nObserved: %s", subject, event.Scope, event.Remaining, event.Currency, event.Threshold, event.Currency, event.ObservedAt.Format(time.RFC3339))
	if channel.Provider == "email" {
		if s.balanceNotificationEmail == nil {
			return errors.New("email unavailable")
		}
		config, err := s.balanceNotificationEmail.GetSMTPConfig(ctx)
		if err != nil {
			return err
		}
		return s.balanceNotificationEmail.sendEmailWithConfig(ctx, config, channel.Email, subject, "<pre>"+html.EscapeString(text)+"</pre>")
	}
	if !s.newAPIFixedKey || s.newAPIEncryptor == nil {
		return ErrNewAPIEncryptionKey
	}
	endpoint, err := s.newAPIEncryptor.Decrypt(channel.urlCipher)
	if err != nil {
		return errors.New("notification decryption failed")
	}
	if err := validateBalanceNotificationURL(channel.Provider, endpoint); err != nil {
		return err
	}
	secret := ""
	if channel.secretCipher != "" {
		secret, err = s.newAPIEncryptor.Decrypt(channel.secretCipher)
		if err != nil {
			return errors.New("notification decryption failed")
		}
	}
	if s.balanceNotificationRepo != nil {
		sum := sha256.Sum256([]byte(endpoint))
		limiter, ok := s.balanceNotificationRepo.(interface {
			ReserveDestination(context.Context, string) error
		})
		if ok {
			if err := limiter.ReserveDestination(ctx, hex.EncodeToString(sum[:])); err != nil {
				return err
			}
		}
	}
	if channel.ID != "" {
		latest, err := s.GetBalanceNotificationSettings(ctx)
		if err != nil {
			return err
		}
		unchanged := false
		for _, current := range latest.Channels {
			if current.ID == channel.ID && current.revision == channel.revision && (current.Enabled || event.Phase == "test") {
				unchanged = true
				break
			}
		}
		if !unchanged {
			return errors.New("notification destination changed")
		}
		if event.Phase != "test" {
			valid, err := s.balanceNotificationStillValid(ctx, event, latest)
			if err != nil {
				return err
			}
			if !valid {
				return errors.New("notification observation changed")
			}
		}
	}
	var payload map[string]any
	switch channel.Provider {
	case "webhook":
		payload = map[string]any{"text": text, "event": event.Phase, "account_id": event.AccountID, "account_name": event.AccountName, "scope": event.Scope, "currency": event.Currency, "remaining": event.Remaining, "threshold": event.Threshold, "observed_at": event.ObservedAt, "event_id": event.ID}
	case "wecom":
		payload = map[string]any{"msgtype": "text", "text": map[string]any{"content": text}}
	case "dingtalk":
		payload = map[string]any{"msgtype": "text", "text": map[string]any{"content": text}}
		if secret != "" {
			timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			u, _ := url.Parse(endpoint)
			q := u.Query()
			q.Set("timestamp", timestamp)
			q.Set("sign", signBalanceNotification(secret, timestamp+"\n"+secret))
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
	case "feishu":
		payload = map[string]any{"msg_type": "text", "content": map[string]any{"text": text}}
		if secret != "" {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			payload["timestamp"], payload["sign"] = timestamp, signBalanceNotification(timestamp+"\n"+secret, "")
		}
	default:
		return balanceNotificationConfigError()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid notification request")
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.Provider == "webhook" && secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		req.Header.Set("X-Sub2API-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	client := *balanceNotificationHTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return errors.New("notification request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("notification HTTP failure")
	}
	if channel.Provider == "webhook" {
		return nil
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(result) > 16384 {
		return errors.New("invalid notification response")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(result, &fields) != nil {
		return errors.New("invalid notification response")
	}
	codeKey := "errcode"
	if channel.Provider == "feishu" {
		codeKey = "code"
		if _, ok := fields[codeKey]; !ok {
			codeKey = "StatusCode"
		}
	}
	var code *int
	if json.Unmarshal(fields[codeKey], &code) != nil || code == nil || *code != 0 {
		return errors.New("notification rejected")
	}
	return nil
}
