package controllers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/newscred/webhook-broker/config"
)

const (
	headerBrokerSignature = "X-Broker-Signature"
)

var (
	errMissingSignatureHeader  = errors.New("missing X-Broker-Signature header")
	errMalformedSignatureHeader = errors.New("malformed X-Broker-Signature header")
	errTimestampOutsideTolerance = errors.New("request timestamp outside tolerance window")
	errInvalidSignature          = errors.New("invalid signature")
	errBodyReadFailure           = errors.New("could not read request body")
)

// NewHMACMiddleware returns a middleware that verifies HMAC-SHA256 signatures on requests.
// When authConfig.IsGatewayAuthEnabled() is false, the middleware is a no-op passthrough.
func NewHMACMiddleware(authConfig config.GatewayAuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !authConfig.IsGatewayAuthEnabled() {
			return next
		}
		secret := authConfig.GetHMACSharedSecret()
		tolerance := authConfig.GetHMACTimestampTolerance()
		exemptPaths := authConfig.GetAuthExemptPaths()

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check exempt paths
			for _, prefix := range exemptPaths {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Extract and parse signature header
			sigHeader := r.Header.Get(headerBrokerSignature)
			if sigHeader == "" {
				writeStatus(w, http.StatusUnauthorized, errMissingSignatureHeader)
				return
			}
			timestamp, signature, err := parseSignatureHeader(sigHeader)
			if err != nil {
				writeStatus(w, http.StatusUnauthorized, errMalformedSignatureHeader)
				return
			}

			// Validate timestamp
			now := time.Now().Unix()
			if math.Abs(float64(now-timestamp)) > tolerance.Seconds() {
				writeStatus(w, http.StatusForbidden, errTimestampOutsideTolerance)
				return
			}

			// Read body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, errBodyReadFailure)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			// Compute and compare HMAC
			payload := strconv.FormatInt(timestamp, 10) + "." + string(body)
			mac := hmac.New(sha256.New, secret)
			mac.Write([]byte(payload))
			expectedSig := hex.EncodeToString(mac.Sum(nil))

			if !hmac.Equal([]byte(expectedSig), []byte(signature)) {
				writeStatus(w, http.StatusForbidden, errInvalidSignature)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// parseSignatureHeader parses "t={timestamp},v1={signature}" into its components.
func parseSignatureHeader(header string) (timestamp int64, signature string, err error) {
	parts := strings.Split(header, ",")
	if len(parts) != 2 {
		return 0, "", errMalformedSignatureHeader
	}
	var foundTimestamp, foundSignature bool
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			timestamp, err = strconv.ParseInt(strings.TrimPrefix(part, "t="), 10, 64)
			if err != nil {
				return 0, "", errMalformedSignatureHeader
			}
			foundTimestamp = true
		} else if strings.HasPrefix(part, "v1=") {
			signature = strings.TrimPrefix(part, "v1=")
			foundSignature = true
		}
	}
	if !foundTimestamp || !foundSignature {
		return 0, "", errMalformedSignatureHeader
	}
	return timestamp, signature, nil
}

// Generated with assistance from Claude AI
