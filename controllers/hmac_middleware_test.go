package controllers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testGatewayAuthConfig struct {
	mode      string
	secret    []byte
	tolerance time.Duration
	exempt    []string
}

func (c *testGatewayAuthConfig) GetGatewayAuthMode() string               { return c.mode }
func (c *testGatewayAuthConfig) GetHMACSharedSecret() []byte              { return c.secret }
func (c *testGatewayAuthConfig) GetHMACTimestampTolerance() time.Duration { return c.tolerance }
func (c *testGatewayAuthConfig) GetAuthExemptPaths() []string             { return c.exempt }
func (c *testGatewayAuthConfig) IsGatewayAuthEnabled() bool               { return c.mode == "hmac" }

func computeTestSignature(body []byte, secret []byte, timestamp int64) string {
	payload := strconv.FormatInt(timestamp, 10) + "." + string(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, signature)
}

var testSecret = []byte("test-secret-key-for-hmac")

func newHMACConfig() *testGatewayAuthConfig {
	return &testGatewayAuthConfig{
		mode:      "hmac",
		secret:    testSecret,
		tolerance: 300 * time.Second,
		exempt:    []string{"/_status", "/metrics", "/debug/pprof/"},
	}
}

func passthroughHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}

func bodyEchoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	})
}

func TestHMACMiddleware_ModeNone_PassesThrough(t *testing.T) {
	t.Parallel()
	cfg := &testGatewayAuthConfig{mode: "none"}
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodGet, "/channel/test/broadcast", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestHMACMiddleware_ModeHMAC_MissingHeader(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader([]byte(`{"test": true}`)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing X-Broker-Signature header")
}

func TestHMACMiddleware_ModeHMAC_MalformedHeader_NoComma(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader([]byte(`{"test": true}`)))
	req.Header.Set(headerBrokerSignature, "garbage")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "malformed X-Broker-Signature header")
}

func TestHMACMiddleware_ModeHMAC_MalformedHeader_MissingT(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader([]byte(`{"test": true}`)))
	req.Header.Set(headerBrokerSignature, "v1=abc,x=1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "malformed X-Broker-Signature header")
}

func TestHMACMiddleware_ModeHMAC_MalformedHeader_BadTimestamp(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader([]byte(`{"test": true}`)))
	req.Header.Set(headerBrokerSignature, "t=notanumber,v1=abc")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "malformed X-Broker-Signature header")
}

func TestHMACMiddleware_ModeHMAC_ValidSignature(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	body := []byte(`{"event": "test"}`)
	timestamp := time.Now().Unix()
	sig := computeTestSignature(body, testSecret, timestamp)

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader(body))
	req.Header.Set(headerBrokerSignature, sig)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestHMACMiddleware_ModeHMAC_WrongSignature(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	timestamp := time.Now().Unix()
	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader([]byte(`{"test": true}`)))
	req.Header.Set(headerBrokerSignature, fmt.Sprintf("t=%d,v1=deadbeefdeadbeefdeadbeef", timestamp))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid signature")
}

func TestHMACMiddleware_ModeHMAC_ExpiredTimestamp(t *testing.T) {
	t.Parallel()
	cfg := &testGatewayAuthConfig{
		mode:      "hmac",
		secret:    testSecret,
		tolerance: 60 * time.Second,
		exempt:    nil,
	}
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	body := []byte(`{"test": true}`)
	timestamp := time.Now().Unix() - 120
	sig := computeTestSignature(body, testSecret, timestamp)

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader(body))
	req.Header.Set(headerBrokerSignature, sig)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "request timestamp outside tolerance window")
}

func TestHMACMiddleware_ModeHMAC_FutureTimestamp(t *testing.T) {
	t.Parallel()
	cfg := &testGatewayAuthConfig{
		mode:      "hmac",
		secret:    testSecret,
		tolerance: 60 * time.Second,
		exempt:    nil,
	}
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	body := []byte(`{"test": true}`)
	timestamp := time.Now().Unix() + 120
	sig := computeTestSignature(body, testSecret, timestamp)

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader(body))
	req.Header.Set(headerBrokerSignature, sig)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "request timestamp outside tolerance window")
}

func TestHMACMiddleware_ModeHMAC_ExemptPath(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodGet, "/_status", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestHMACMiddleware_ModeHMAC_ExemptPathPrefix(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestHMACMiddleware_ModeHMAC_EmptyBody_GET(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(passthroughHandler())

	timestamp := time.Now().Unix()
	sig := computeTestSignature(nil, testSecret, timestamp)

	req := httptest.NewRequest(http.MethodGet, "/channel/test", nil)
	req.Header.Set(headerBrokerSignature, sig)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHMACMiddleware_ModeHMAC_BodyReadableDownstream(t *testing.T) {
	t.Parallel()
	cfg := newHMACConfig()
	middleware := NewHMACMiddleware(cfg)
	handler := middleware(bodyEchoHandler())

	body := []byte(`{"event": "order.created", "data": {"id": "123"}}`)
	timestamp := time.Now().Unix()
	sig := computeTestSignature(body, testSecret, timestamp)

	req := httptest.NewRequest(http.MethodPost, "/channel/test/broadcast", bytes.NewReader(body))
	req.Header.Set(headerBrokerSignature, sig)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, string(body), rr.Body.String())
}

func TestParseSignatureHeader(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		ts, sig, err := parseSignatureHeader("t=1708200000,v1=abcdef123456")
		assert.Nil(t, err)
		assert.Equal(t, int64(1708200000), ts)
		assert.Equal(t, "abcdef123456", sig)
	})
	t.Run("TooManyParts", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseSignatureHeader("t=123,v1=abc,extra=data")
		assert.Equal(t, errMalformedSignatureHeader, err)
	})
	t.Run("MissingV1", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseSignatureHeader("t=123,v2=abc")
		assert.Equal(t, errMalformedSignatureHeader, err)
	})
	t.Run("MissingT", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseSignatureHeader("ts=123,v1=abc")
		assert.Equal(t, errMalformedSignatureHeader, err)
	})
	t.Run("NonIntegerTimestamp", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseSignatureHeader("t=abc,v1=def")
		assert.Equal(t, errMalformedSignatureHeader, err)
	})
}

// Generated with assistance from Claude AI
