package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const monitorProbeHeader = "X-Sub2-Monitor-Probe"
const monitorProbeAttemptTimeout = 12 * time.Second
const monitorProbeTotalTimeout = 36 * time.Second

type monitorProbeKey struct{}

func MonitorProbe(ctx context.Context) bool { return ctx.Value(monitorProbeKey{}) != nil }

func monitorProbeSignature(req *http.Request, stamp, digest, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	credential := strings.Join([]string{req.Header.Get("Authorization"), req.Header.Get("x-api-key"), req.Header.Get("x-goog-api-key")}, "\n")
	fmt.Fprintf(mac, "sub2-monitor-v1\n%s\n%s\n%s\n%s\n%s\n%s", stamp, req.Method,
		host, req.URL.RequestURI(), credential, digest)
	return hex.EncodeToString(mac.Sum(nil))
}

// Authenticate observation separately from normal API key authorization.
// Bind the marker to this exact request, never a user-supplied boolean header.
func AuthenticateMonitorProbe(req *http.Request, secret string) {
	value := req.Header.Get(monitorProbeHeader)
	req.Header.Del(monitorProbeHeader)
	parts := strings.Split(value, ".")
	if len(secret) < 32 || len(parts) != 3 || len(parts[1]) != 64 || len(parts[2]) != 64 {
		return
	}
	stamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Since(time.Unix(stamp, 0)) > time.Minute || time.Until(time.Unix(stamp, 0)) > 5*time.Second {
		return
	}
	want := monitorProbeSignature(req, parts[0], parts[1], secret)
	if !hmac.Equal([]byte(want), []byte(parts[2])) || req.Body == nil {
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, monitorResponseMaxBytes+1))
	req.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(body), req.Body), req.Body}
	if err != nil || len(body) > monitorResponseMaxBytes {
		return
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != parts[1] {
		return
	}
	ctx := context.WithValue(req.Context(), monitorProbeKey{}, true)
	ctx = context.WithValue(ctx, qualitySchedulingReadOnlyKey{}, true)
	*req = *req.WithContext(ctx)
}
