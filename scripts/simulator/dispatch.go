package main

import (
	"bytes"
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

// SendRequest defines the payload received by the dispatch proxy.
type SendRequest struct {
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
	SignProvider string            `json:"sign_provider,omitempty"`
	SignSecret   string            `json:"sign_secret,omitempty"`
}

// SendResponse defines the execution result returned to the UI.
type SendResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	DurationMS float64           `json:"duration_ms"`
	Error      string            `json:"error,omitempty"`
}

// ComputeStripeSignature computes a valid Stripe-Signature header value: t=<ts>,v1=<sig>.
func ComputeStripeSignature(secret string, body []byte, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%s,v1=%s", ts, sig)
}

// ComputeGitHubSignature computes a valid X-Hub-Signature-256 header value: sha256=<sig>.
func ComputeGitHubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("sha256=%s", sig)
}

// Dispatch executes the HTTP request against Simple Relay on behalf of the browser UI.
func Dispatch(client *http.Client, req SendRequest) (*SendResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodPost
	}

	bodyBytes := []byte(req.Body)

	// Apply dynamic cryptographic signatures if requested
	headers := make(map[string]string)
	for k, v := range req.Headers {
		headers[k] = v
	}

	provider := strings.ToLower(strings.TrimSpace(req.SignProvider))
	secret := strings.TrimSpace(req.SignSecret)

	switch provider {
	case "stripe":
		if secret != "" {
			headers["Stripe-Signature"] = ComputeStripeSignature(secret, bodyBytes, time.Now())
		}
	case "github":
		if secret != "" {
			headers["X-Hub-Signature-256"] = ComputeGitHubSignature(secret, bodyBytes)
		}
	}

	httpReq, err := http.NewRequest(method, req.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, doErr := client.Do(httpReq)
	durationMs := float64(time.Since(start).Microseconds()) / 1000.0

	result := &SendResponse{
		Headers:    make(map[string]string),
		DurationMS: durationMs,
	}

	if doErr != nil {
		result.Error = doErr.Error()
		return result, nil
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	for k, vv := range resp.Header {
		result.Headers[k] = strings.Join(vv, ", ")
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB max response
	if err == nil {
		result.Body = string(respBody)
	}

	return result, nil
}
