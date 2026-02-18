# Implementation Plan: HMAC Request Authentication

Tech spec: [docs/tech-specs/hmac-auth.md](../tech-specs/hmac-auth.md)

## Step 1: Configuration Interface and Parsing

### 1a. Create `config/gateway_auth_config.go`

New file. Define the interface and implement it on `*Config`.

Follow the same pattern as `config/dlq_config.go` and `config/scheduler_config.go`:
interface definition, constants for defaults, and method implementations on `*Config`.

```go
package config

import (
	"encoding/base64"
	"strings"
	"time"
)

const (
	DefaultGatewayAuthMode                    = "none"
	DefaultHMACTimestampToleranceSeconds uint = 300
	DefaultAuthExemptPaths                    = "/_status,/metrics,/debug/pprof/"
)

// GatewayAuthConfig represents configuration for gateway-level authentication
type GatewayAuthConfig interface {
	// GetGatewayAuthMode returns the authentication mode ("none" or "hmac")
	GetGatewayAuthMode() string
	// GetHMACSharedSecret returns the base64-decoded shared secret for HMAC signing
	GetHMACSharedSecret() []byte
	// GetHMACTimestampTolerance returns the maximum age of a signed request
	GetHMACTimestampTolerance() time.Duration
	// GetAuthExemptPaths returns path prefixes that bypass gateway auth
	GetAuthExemptPaths() []string
	// IsGatewayAuthEnabled returns true if gateway auth is active
	IsGatewayAuthEnabled() bool
}
```

Method implementations on `*Config`:

- `GetGatewayAuthMode()` — returns `config.GatewayAuthMode`, defaulting to `"none"` if empty.
- `GetHMACSharedSecret()` — calls `base64.StdEncoding.DecodeString(config.HMACSharedSecret)` and returns the raw bytes. Return `nil` if decoding fails (validation catches this at startup). The middleware captures the return value once at init time, so repeated decoding is not a concern.
- `GetHMACTimestampTolerance()` — returns `time.Duration(config.HMACTimestampToleranceSeconds) * time.Second`, defaulting to `DefaultHMACTimestampToleranceSeconds` if zero.
- `GetAuthExemptPaths()` — splits `config.AuthExemptPaths` on `,`, trims whitespace from each entry, returns the slice. Defaults to `DefaultAuthExemptPaths` if empty.
- `IsGatewayAuthEnabled()` — returns `config.GetGatewayAuthMode() == "hmac"`.

### 1b. Add fields to `Config` struct in `config/config.go`

Add these fields to the `Config` struct (line ~131, after the DLQ fields):

```go
// Gateway auth configuration
GatewayAuthMode                 string
HMACSharedSecret                string
HMACTimestampToleranceSeconds   uint
AuthExemptPaths                 string
```

### 1c. Add `setupGatewayAuthConfiguration` in `config/config.go`

Add a new setup function following the exact pattern of `setupDLQConfiguration` (lines 657-663):

```go
func setupGatewayAuthConfiguration(cfg *ini.File, configuration *Config) {
	gatewayAuthSection, err := cfg.GetSection("gateway-auth")
	if err != nil {
		return
	}
	configuration.GatewayAuthMode = gatewayAuthSection.Key("mode").MustString(DefaultGatewayAuthMode)
	configuration.HMACSharedSecret = gatewayAuthSection.Key("hmac-shared-secret").MustString("")
	configuration.HMACTimestampToleranceSeconds = gatewayAuthSection.Key("hmac-timestamp-tolerance-seconds").MustUint(DefaultHMACTimestampToleranceSeconds)
	configuration.AuthExemptPaths = gatewayAuthSection.Key("auth-exempt-paths").MustString(DefaultAuthExemptPaths)
}
```

Call it from `GetConfigurationFromParseConfig` (line ~348), after `setupDLQConfiguration`:

```go
setupGatewayAuthConfiguration(cfg, configuration)
```

### 1d. Add validation in `validateConfigurationState`

In `validateConfigurationState` (config/config.go, line ~355), add validation before the expensive port/DB checks:

```go
if configuration.GatewayAuthMode == "hmac" {
	if len(configuration.HMACSharedSecret) == 0 {
		return errors.New("gateway-auth: hmac-shared-secret is required when mode=hmac")
	}
	if _, err := base64.StdEncoding.DecodeString(configuration.HMACSharedSecret); err != nil {
		return fmt.Errorf("gateway-auth: hmac-shared-secret is not valid base64: %w", err)
	}
} else if configuration.GatewayAuthMode != "none" && configuration.GatewayAuthMode != "" {
	return fmt.Errorf("gateway-auth: unsupported mode %q (must be \"none\" or \"hmac\")", configuration.GatewayAuthMode)
}
```

Add `"encoding/base64"` to imports if not already present (it is — line 7).

### 1e. Add defaults to `config/defaultconfig.go`

Append the `[gateway-auth]` section to the `DefaultConfiguration` const (after the `[sample-consumer]` section, before the closing backtick):

```ini
[gateway-auth]
mode=none
hmac-shared-secret=
hmac-timestamp-tolerance-seconds=300
auth-exempt-paths=/_status,/metrics,/debug/pprof/
```

### 1f. Add wire binding in `config/config.go`

Update `ConfigInjector` (line 76) to add the new binding. Append to the existing `wire.NewSet`:

```go
wire.Bind(new(GatewayAuthConfig), new(*Config))
```

The full line becomes:
```go
ConfigInjector = wire.NewSet(GetConfigurationFromCLIConfig, wire.Bind(new(SeedDataConfig), new(*Config)), wire.Bind(new(HTTPConfig), new(*Config)), wire.Bind(new(RelationalDatabaseConfig), new(*Config)), wire.Bind(new(LogConfig), new(*Config)), wire.Bind(new(BrokerConfig), new(*Config)), wire.Bind(new(ConsumerConnectionConfig), new(*Config)), wire.Bind(new(SchedulerConfig), new(*Config)), wire.Bind(new(DLQConfig), new(*Config)), wire.Bind(new(GatewayAuthConfig), new(*Config)))
```

---

## Step 2: HMAC Middleware

### 2a. Create `controllers/hmac_middleware.go`

New file. Contains the middleware factory and signature parsing/verification logic.

**Constants and errors:**

```go
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
	errMissingSignatureHeader = errors.New("missing X-Broker-Signature header")
	errMalformedSignatureHeader = errors.New("malformed X-Broker-Signature header")
	errTimestampOutsideTolerance = errors.New("request timestamp outside tolerance window")
	errInvalidSignature = errors.New("invalid signature")
	errBodyReadFailure = errors.New("could not read request body")
)
```

**`NewHMACMiddleware` function:**

```go
func NewHMACMiddleware(authConfig config.GatewayAuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !authConfig.IsGatewayAuthEnabled() {
			return next
		}
		secret := authConfig.GetHMACSharedSecret()
		tolerance := authConfig.GetHMACTimestampTolerance()
		exemptPaths := authConfig.GetAuthExemptPaths()

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Check exempt paths
			for _, prefix := range exemptPaths {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// 2. Extract and parse signature header
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

			// 3. Validate timestamp
			now := time.Now().Unix()
			if math.Abs(float64(now-timestamp)) > tolerance.Seconds() {
				writeStatus(w, http.StatusForbidden, errTimestampOutsideTolerance)
				return
			}

			// 4. Read body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeStatus(w, http.StatusInternalServerError, errBodyReadFailure)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			// 5. Compute and compare HMAC
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
```

**`parseSignatureHeader` helper:**

```go
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
```

### 2b. Modify `controllers/router.go`

**Update `getHandler` signature and body** (line 172):

Current:
```go
func getHandler(apiRouter *httprouter.Router) http.Handler {
	return hlog.NewHandler(log.Logger)(getRequestIDHandler(requestIDLogFieldKey, headerRequestID)(hlog.AccessHandler(logAccess)(apiRouter)))
}
```

New:
```go
func getHandler(apiRouter *httprouter.Router, authConfig config.GatewayAuthConfig) http.Handler {
	hmacMiddleware := NewHMACMiddleware(authConfig)
	return hlog.NewHandler(log.Logger)(getRequestIDHandler(requestIDLogFieldKey, headerRequestID)(hlog.AccessHandler(logAccess)(hmacMiddleware(apiRouter))))
}
```

This places the HMAC middleware inside the access log handler but outside the router. In Go's middleware nesting, the outermost handler runs first and also captures the response. `hlog.AccessHandler` wraps the response writer, calls the inner handler, then logs the response status — so it must be **outside** the HMAC middleware to capture 401/403 rejections in the access log. The chain executes as:
```
Request → Logger → Request ID → Access Log (wraps response) → HMAC Middleware → Router → Controller
```

**Update `ConfigureAPI` signature** (line 178):

Current:
```go
func ConfigureAPI(httpConfig config.HTTPConfig, iListener ServerLifecycleListener, apiRouter *httprouter.Router) *http.Server {
```

New:
```go
func ConfigureAPI(httpConfig config.HTTPConfig, iListener ServerLifecycleListener, apiRouter *httprouter.Router, authConfig config.GatewayAuthConfig) *http.Server {
```

Update the call inside (line 180):
```go
handler := getHandler(apiRouter, authConfig)
```

**Update `createTestRouter` in `controllers/status_test.go`** (line 58):

The test helper calls `getHandler`. It needs updating to pass a no-op config:

Current:
```go
func createTestRouter(endpoints ...EndpointController) http.Handler {
	testRouter := httprouter.New()
	setupAPIRoutes(testRouter, endpoints...)
	return getHandler(testRouter)
}
```

New:
```go
func createTestRouter(endpoints ...EndpointController) http.Handler {
	testRouter := httprouter.New()
	setupAPIRoutes(testRouter, endpoints...)
	return getHandler(testRouter, &noopGatewayAuthConfig{})
}
```

Add a minimal stub in the test file (or a shared test helper):

```go
type noopGatewayAuthConfig struct{}

func (c *noopGatewayAuthConfig) GetGatewayAuthMode() string              { return "none" }
func (c *noopGatewayAuthConfig) GetHMACSharedSecret() []byte             { return nil }
func (c *noopGatewayAuthConfig) GetHMACTimestampTolerance() time.Duration { return 0 }
func (c *noopGatewayAuthConfig) GetAuthExemptPaths() []string            { return nil }
func (c *noopGatewayAuthConfig) IsGatewayAuthEnabled() bool              { return false }
```

This ensures all existing controller tests continue to work without changes since the middleware no-ops when `IsGatewayAuthEnabled()` returns false.

---

## Step 3: Wire Regeneration

### 3a. Regenerate `wire_gen.go`

After the changes to `ConfigureAPI`'s signature and the new `GatewayAuthConfig` binding in `ConfigInjector`, the wire-generated code needs to be updated.

The generated `GetHTTPServer` function (line 105 of `wire_gen.go`) currently calls:
```go
server := controllers.ConfigureAPI(configConfig, serverLifecycleListenerImpl, router)
```

After regeneration it should become:
```go
server := controllers.ConfigureAPI(configConfig, serverLifecycleListenerImpl, router, configConfig)
```

Wire resolves `config.GatewayAuthConfig` to `*config.Config` via the binding added in step 1f. The fourth argument `configConfig` is the same `*config.Config` instance already in scope.

Run:
```bash
make dep-tools  # ensures wire tool is installed
go generate ./...
```

Or directly:
```bash
wire ./...
```

Verify the generated file compiles and the `ConfigureAPI` call has four arguments.

---

## Step 4: Unit Tests

### 4a. Create `controllers/hmac_middleware_test.go`

New file. Test the middleware in isolation using `httptest.NewRecorder` and a simple pass-through handler, following the patterns in `controllers/status_test.go` and `controllers/broadcast_test.go`.

**Test helper to create a signed request:**

```go
func computeTestSignature(body []byte, secret []byte, timestamp int64) string {
	payload := strconv.FormatInt(timestamp, 10) + "." + string(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, signature)
}
```

**Test helper to create a mock config:**

```go
type testGatewayAuthConfig struct {
	mode      string
	secret    []byte
	tolerance time.Duration
	exempt    []string
}

func (c *testGatewayAuthConfig) GetGatewayAuthMode() string              { return c.mode }
func (c *testGatewayAuthConfig) GetHMACSharedSecret() []byte             { return c.secret }
func (c *testGatewayAuthConfig) GetHMACTimestampTolerance() time.Duration { return c.tolerance }
func (c *testGatewayAuthConfig) GetAuthExemptPaths() []string            { return c.exempt }
func (c *testGatewayAuthConfig) IsGatewayAuthEnabled() bool              { return c.mode == "hmac" }
```

**Test cases (each as a `t.Run` subtest):**

| Test Name | Setup | Request | Expected |
|---|---|---|---|
| `ModeNone_PassesThrough` | mode=none | No signature header | 200 (pass-through) |
| `ModeHMAC_MissingHeader` | mode=hmac | No signature header | 401 |
| `ModeHMAC_MalformedHeader_NoComma` | mode=hmac | `X-Broker-Signature: garbage` | 401 |
| `ModeHMAC_MalformedHeader_MissingT` | mode=hmac | `X-Broker-Signature: v1=abc,x=1` | 401 |
| `ModeHMAC_MalformedHeader_BadTimestamp` | mode=hmac | `X-Broker-Signature: t=notanumber,v1=abc` | 401 |
| `ModeHMAC_ValidSignature` | mode=hmac, known secret | Correctly signed POST with body | 200, body readable downstream |
| `ModeHMAC_WrongSignature` | mode=hmac | Wrong v1 value | 403, "invalid signature" |
| `ModeHMAC_ExpiredTimestamp` | mode=hmac, tolerance=60s | Timestamp 120s in the past | 403, "timestamp outside tolerance" |
| `ModeHMAC_FutureTimestamp` | mode=hmac, tolerance=60s | Timestamp 120s in the future | 403, "timestamp outside tolerance" |
| `ModeHMAC_ExemptPath` | mode=hmac, exempt=["/_status"] | GET /_status, no signature | 200 |
| `ModeHMAC_ExemptPathPrefix` | mode=hmac, exempt=["/debug/pprof/"] | GET /debug/pprof/heap, no signature | 200 |
| `ModeHMAC_EmptyBody_GET` | mode=hmac | GET with signature over `{timestamp}.` | 200 |
| `ModeHMAC_BodyReadableDownstream` | mode=hmac | Valid signed POST | Downstream handler reads full body, content matches |

For the "body readable downstream" test, the pass-through handler should `io.ReadAll(r.Body)` and verify the content matches the original request body.

### 4b. Create `config/gateway_auth_config_test.go`

New file. Follow the patterns in `config/config_test.go`:
- Use `loadTestConfiguration(testConfigString)` to create `*ini.File`
- Call `GetConfigurationFromParseConfig(cfg)` to get the `*Config`
- Assert getter return values

**Test cases:**

| Test Name | Config Input | Assertion |
|---|---|---|
| `TestDefaultGatewayAuthMode` | No `[gateway-auth]` section | `GetGatewayAuthMode() == "none"`, `IsGatewayAuthEnabled() == false` |
| `TestHMACModeWithValidSecret` | `mode=hmac`, `hmac-shared-secret=dGVzdA==` | `IsGatewayAuthEnabled() == true`, `GetHMACSharedSecret()` returns decoded bytes |
| `TestHMACModeWithMissingSecret` | `mode=hmac`, no secret | `GetConfigurationFromParseConfig` returns error containing "hmac-shared-secret is required" |
| `TestHMACModeWithInvalidBase64` | `mode=hmac`, `hmac-shared-secret=!!!notbase64` | Returns error containing "not valid base64" |
| `TestInvalidMode` | `mode=foobar` | Returns error containing "unsupported mode" |
| `TestTimestampToleranceDefault` | `mode=none` | `GetHMACTimestampTolerance() == 300 * time.Second` |
| `TestTimestampToleranceCustom` | `hmac-timestamp-tolerance-seconds=120` | `GetHMACTimestampTolerance() == 120 * time.Second` |
| `TestExemptPathsDefault` | No `auth-exempt-paths` key | `GetAuthExemptPaths()` returns `["/_status", "/metrics", "/debug/pprof/"]` |
| `TestExemptPathsCustom` | `auth-exempt-paths=/_status,/health` | `GetAuthExemptPaths()` returns `["/_status", "/health"]` |
| `TestConfigImplementsInterface` | None | `var _ GatewayAuthConfig = (*Config)(nil)` compiles |

Note: the gateway auth validation is placed at the top of `validateConfigurationState`, before the port-listening and DB-connection checks. This means `GetConfigurationFromParseConfig(loadTestConfiguration(configStr))` will return the gateway auth validation error before attempting to open a port or connect to a database, so tests can call it directly without overriding `loadConfiguration` or needing a running database.

---

## Step 5: Default Config Update for Controller Tests

### 5a. Update `controllers/controller-test-config.cfg`

This file currently only has:
```ini
[http]
listener=:17654
```

No changes needed here — the `[gateway-auth]` section defaults will apply (mode=none), so no HMAC enforcement during existing controller tests.

---

## Step 6: Integration Tests

### 6a. Overview

The HMAC integration tests need a separate broker instance with `mode=hmac` enabled. The existing integration test suite must continue running against the non-HMAC broker unchanged.

### 6b. Create `webhook-broker-integration-test-hmac.cfg`

New file in the project root. Copy from `webhook-broker-integration-test.cfg` and add:

```ini
[rdbms]
dialect=mysql
connection-url=webhook_broker:zxc909zxc@tcp(mysql:3306)/webhook-broker?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true&multiStatements=true

[http]
listener=:8082

[broker]
max-message-queue-size=10000000
max-workers=250
retry-backoff-delays-in-seconds=1
rational-delay-in-seconds=1
retrigger-base-endpoint=http://localhost:8082

[consumer-connection]
connection-timeout-in-seconds=10

[log]
log-level=error

[gateway-auth]
mode=hmac
hmac-shared-secret=dGVzdC1obWFjLXNlY3JldC1mb3ItaW50ZWdyYXRpb24=
hmac-timestamp-tolerance-seconds=300
auth-exempt-paths=/_status,/metrics,/debug/pprof/

[initial-channels]
hmac-test-channel=HMAC Test Channel

[initial-producers]
hmac-test-producer=HMAC Test Producer

[initial-channel-tokens]
hmac-test-channel=hmac-test-channel-token

[initial-producer-tokens]
hmac-test-producer=hmac-test-producer-token
```

Key differences from the main integration test config:
- Listens on `:8082` (separate port from the main broker on `:8080`)
- Has `[gateway-auth]` with `mode=hmac` and a known secret
- Has its own seed channel/producer for isolated testing
- Shares the same MySQL database (the broker tables can coexist)

### 6c. Add HMAC broker service to `docker-compose.integration-test.yaml`

Add a new service after `ibroker`:

```yaml
ibroker-hmac:
  build: .
  container_name: ibroker-hmac
  volumes:
    - ./webhook-broker-integration-test-hmac.cfg:/webhook-broker.cfg
  command: ["./webhook-broker", "-migrate", "./migration/sqls/"]
  depends_on:
    ibroker:
      condition: service_healthy
  ports:
    - "31820:8082"
  healthcheck:
    test: ["CMD", "curl", "-f", "http://localhost:8082/_status"]
    interval: 5s
    timeout: 10s
    retries: 20
    start_period: 10s
  networks:
    brokeritest:
      aliases:
        - "webhook-broker-hmac"
```

Update the `tester` service's `depends_on` to include `ibroker-hmac`:

```yaml
tester:
  build: integration-test/.
  container_name: tester
  command: ["make", "run"]
  depends_on:
    ibroker:
      condition: service_healthy
    ibroker-hmac:
      condition: service_healthy
    ipruner:
      condition: service_started
  networks:
    brokeritest:
      aliases:
        - "tester"
```

### 6d. Add HMAC tests to `integration-test/main.go`

Add a new env var and constant:

```go
const (
	defaultHMACBrokerBaseURL = "http://webhook-broker-hmac:8082"
)

var hmacBrokerBaseURL string
```

In `main()`, read it and call the test:

```go
hmacBrokerBaseURL = getEnvOrDefault("HMAC_BROKER_BASE_URL", defaultHMACBrokerBaseURL)
// ... after existing tests
testHMACAuth()
```

Add new test constants:

```go
const (
	hmacChannelID     = "hmac-test-channel"
	hmacChannelToken  = "hmac-test-channel-token"
	hmacProducerID    = "hmac-test-producer"
	hmacProducerToken = "hmac-test-producer-token"
	// Must match the base64-encoded secret in webhook-broker-integration-test-hmac.cfg
	hmacSharedSecretB64 = "dGVzdC1obWFjLXNlY3JldC1mb3ItaW50ZWdyYXRpb24="
)
```

Add a `computeSignature` helper (same logic as the unit test helper).

Add `testHMACAuth()` function with these sub-tests:

1. **Exempt path without signature**: `GET /_status` on the HMAC broker — expect 200.
2. **Missing signature**: `POST /channel/{channelId}/broadcast` without `X-Broker-Signature` — expect 401.
3. **Wrong signature**: POST with `X-Broker-Signature: t={now},v1=deadbeef...` — expect 403.
4. **Expired timestamp**: POST with valid HMAC but timestamp 600s in the past — expect 403.
5. **Valid signed broadcast**: POST with correct HMAC signature and all broker headers — expect 201.

Each sub-test should log success/failure and call `os.Exit` on failure, matching the existing integration test error handling pattern.

---

## Step 7: Documentation

### 7a. Update `docs/configuration.md`

Add a new section for `[gateway-auth]`:

```markdown
### Gateway Authentication (`[gateway-auth]`)

| Key | Default | Description |
|---|---|---|
| `mode` | `none` | Authentication mode. `none` disables gateway auth (current behavior). `hmac` enables HMAC-SHA256 signature verification on all non-exempt requests. |
| `hmac-shared-secret` | (empty) | Base64-encoded shared secret for HMAC signing. Required when `mode=hmac`. Generate with `openssl rand -base64 32`. |
| `hmac-timestamp-tolerance-seconds` | `300` | Maximum age (in seconds) of a signed request before rejection. Protects against replay attacks. |
| `auth-exempt-paths` | `/_status,/metrics,/debug/pprof/` | Comma-separated list of URL path prefixes that bypass gateway authentication. |
```

Include the signature format, a brief explanation of how it works, and a curl example (from the tech spec).

### 7b. Update `CLAUDE.md`

Add a new section after the DLQ Observability section:

```markdown
## Gateway Authentication (HMAC)

Optional HMAC-SHA256 request authentication for public-facing deployments.

### Configuration

`[gateway-auth]` INI section: `mode` (`none`|`hmac`), `hmac-shared-secret` (base64), `hmac-timestamp-tolerance-seconds` (default 300), `auth-exempt-paths` (default `/_status,/metrics,/debug/pprof/`).

Interface: `config.GatewayAuthConfig`. Middleware: `controllers.NewHMACMiddleware`.

### Signature Format

Header: `X-Broker-Signature: t={unix_timestamp},v1={hex_hmac_sha256}`
Signing payload: `{timestamp}.{request_body}`
Secret is base64-decoded before use as HMAC key.

### Middleware Chain Position

`Logger → Request ID → Access Log (wraps response) → HMAC Middleware → Router → Controller`

Access log handler wraps around HMAC middleware so that 401/403 rejections are recorded with request IDs.
```

---

## Step 8: Verify

### 8a. Run unit tests

```bash
make test
```

All existing tests must pass. The `createTestRouter` helper uses `noopGatewayAuthConfig` so no existing controller tests are affected.

### 8b. Run integration tests

```bash
make itest
```

The existing test suite runs against the non-HMAC broker as before. The new `testHMACAuth` runs against the HMAC broker on port 8082.

### 8c. Manual smoke test

Start a local broker with HMAC enabled and verify with curl:

```bash
# Generate a test config with HMAC enabled
cat >> /tmp/hmac-test.cfg <<EOF
[gateway-auth]
mode=hmac
hmac-shared-secret=$(openssl rand -base64 32)
EOF

# Start broker
./webhook-broker -config /tmp/hmac-test.cfg

# Verify /_status is exempt (should return 200)
curl -s http://localhost:8080/_status

# Verify broadcast without signature is rejected (should return 401)
curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8080/channel/sample-channel/broadcast

# Verify broadcast with valid signature is accepted (use the curl example from tech spec)
```

---

## File Change Summary

| File | Action | Description |
|---|---|---|
| `config/gateway_auth_config.go` | **Create** | Interface definition, constants, method implementations |
| `config/gateway_auth_config_test.go` | **Create** | Config parsing and validation tests |
| `config/config.go` | **Modify** | Add struct fields, setup function call, validation, wire binding |
| `config/defaultconfig.go` | **Modify** | Add `[gateway-auth]` default section |
| `controllers/hmac_middleware.go` | **Create** | Middleware factory, signature parsing, HMAC verification |
| `controllers/hmac_middleware_test.go` | **Create** | Middleware unit tests |
| `controllers/router.go` | **Modify** | Update `getHandler` and `ConfigureAPI` signatures |
| `controllers/status_test.go` | **Modify** | Update `createTestRouter` to pass `noopGatewayAuthConfig` |
| `wire_gen.go` | **Regenerate** | Wire picks up new `ConfigureAPI` parameter |
| `webhook-broker-integration-test-hmac.cfg` | **Create** | HMAC-enabled broker config for integration tests |
| `docker-compose.integration-test.yaml` | **Modify** | Add `ibroker-hmac` service |
| `integration-test/main.go` | **Modify** | Add `testHMACAuth` function |
| `docs/configuration.md` | **Modify** | Document `[gateway-auth]` section |
| `CLAUDE.md` | **Modify** | Add gateway auth section for AI context |

## Dependency Order

Steps must be executed in order: 1 → 2 → 3 → 4/5 (parallel) → 6 → 7 → 8.

Step 1 (config) must be complete before step 2 (middleware) because the middleware depends on `config.GatewayAuthConfig`. Step 3 (wire) depends on both 1 and 2. Steps 4 (unit tests) and 5 (test config) can be done in parallel. Step 6 (integration tests) requires a buildable binary so it depends on 1-3. Step 7 (docs) can be done anytime but is listed last for logical flow. Step 8 (verification) is always last.
