# Tech Spec: HMAC Request Authentication

|               |                    |
| ------------- | ------------------ |
| _Version_     | 1                  |
| _Date_        | February 17, 2026  |
| _Last Update_ | February 17, 2026  |

## Problem Statement / Goal

Webhook-broker's current authentication model uses shared secret tokens passed in HTTP headers (`X-Broker-Channel-Token`, `X-Broker-Producer-Token`). This works well for trusted internal networks, but is insufficient for deployments where the broker is exposed to the public internet — for example, receiving events from external systems that push data into the broker.

Static tokens have several limitations in this context:

- **No payload integrity**: A token proves identity but does not verify the request body was not tampered with in transit.
- **Replay vulnerability**: A captured request can be replayed as-is at any time.
- **Secret in every request**: The token is sent verbatim in every HTTP header, increasing the window for credential leakage through logs, proxies, or monitoring systems.

The goal is to add an optional HMAC (Hash-based Message Authentication Code) authentication layer to webhook-broker so it can be safely deployed as a public-facing event ingestion gateway, with request integrity verification and replay protection, without removing the existing token-based authentication.

## Background: HMAC Authentication

### What is HMAC?

HMAC is a mechanism for computing a message authentication code using a cryptographic hash function (e.g., SHA-256) combined with a secret key. Both the sender and receiver share the secret key, but the secret is never transmitted — only the resulting signature is sent with the request.

The signature is computed over request content (typically the body, timestamp, and other request attributes), so the receiver can independently recompute it and compare. If they match, the request is authentic and unmodified.

### How It Works (Request Flow)

**Sender (producer/client):**

1. Read the request body
2. Get the current Unix timestamp
3. Construct a signing payload: `{timestamp}.{body}`
4. Compute `HMAC-SHA256(signing_payload, shared_secret)`
5. Send the request with the signature header:
   ```
   X-Broker-Signature: t={timestamp},v1={hex(signature)}
   ```

**Receiver (webhook-broker):**

1. Extract `t` (timestamp) and `v1` (signature) from the header
2. Check that the timestamp is within an acceptable tolerance window (e.g., 5 minutes)
3. Reconstruct the signing payload: `{timestamp}.{body}`
4. Compute `HMAC-SHA256(signing_payload, configured_secret)`
5. Compare the computed signature with `v1` using constant-time comparison
6. Accept or reject the request

### Pros

- **Payload integrity**: The body is included in the signature, so any modification (by a proxy, attacker, or bug) is detected.
- **Replay protection**: The timestamp component means captured requests expire after the tolerance window. An attacker who intercepts a request has a limited window to replay it, and the body cannot be changed.
- **Secret never transmitted**: Unlike bearer tokens, the shared secret never appears in any HTTP header or wire traffic. Only the derived signature is sent.
- **Industry standard pattern**: This is the same pattern used by Stripe, GitHub, Twilio, Shopify, and others for webhook verification. Client libraries and documentation are widely available.
- **Simple client implementation**: Computing HMAC-SHA256 is a one-liner in every major language. No complex key exchange, certificate management, or OAuth flows required.
- **Layered security**: Can operate alongside the existing channel/producer token validation as defense in depth — HMAC verifies the request came from someone with the secret, tokens verify the producer/channel identity.

### Cons

- **Clock synchronization**: Both sender and receiver must have reasonably synchronized clocks. A 5-minute tolerance window makes this a non-issue in practice, but severely misconfigured clocks will cause rejections.
- **Shared secret management**: Both parties must securely store the same secret. If the secret is compromised, it must be rotated on both sides. (This is also true of the existing token model.)
- **No identity granularity**: A single shared secret authenticates all requests the same way. It does not distinguish between individual producers. (The existing producer token system provides per-producer identity on top of this.)
- **Request body must be buffered**: The broker must read the full request body before routing, in order to verify the signature. This is already the case in the broadcast controller, so there is no additional cost.

### Why HMAC Over Other Options

| Approach | Pros | Cons |
|---|---|---|
| **Bearer Token** | Simplest to implement | No payload integrity, no replay protection, secret on the wire |
| **HMAC Signature** | Payload integrity, replay protection, secret never on wire | Slightly more complex client code |
| **mTLS** | Strongest authentication | Complex certificate management, not practical for many external clients |
| **OAuth 2.0 / JWT** | Standard identity protocol | Requires token issuance infrastructure, over-engineered for machine-to-machine event ingestion |

HMAC is the right balance for webhook-broker: it provides meaningful security improvements over bearer tokens without requiring infrastructure changes (no certificate authority, no token server). Clients compute a signature in a few lines of code.

## Design

### Scope

HMAC authentication will be applied as a **gateway-level middleware** that runs before any controller logic. It protects the broadcast endpoint (and optionally all endpoints) from unauthenticated requests when enabled.

The existing per-producer/per-channel token validation remains unchanged and continues to operate as the application-level identity check. HMAC is the perimeter check; tokens are the authorization check.

### Configuration

New INI section `[gateway-auth]`:

```ini
[gateway-auth]
# Authentication mode: none (default, current behavior), hmac
mode=none

# Shared secret for HMAC signing. Must be a base64-encoded string.
# Generate with: openssl rand -base64 32
hmac-shared-secret=

# Maximum age of a signed request in seconds before it is rejected.
# Protects against replay attacks. Default: 300 (5 minutes).
hmac-timestamp-tolerance-seconds=300

# Comma-separated list of path prefixes that bypass gateway auth.
# Useful for health checks, metrics, and profiling endpoints that are
# consumed by internal infrastructure (Prometheus, profilers) which
# won't compute HMAC signatures. In production, these paths should
# also be blocked at the proxy/firewall level so they are not
# reachable from the public internet.
# Default: /_status,/metrics,/debug/pprof/
auth-exempt-paths=/_status,/metrics,/debug/pprof/
```

When `mode=none`, behavior is identical to today — no middleware is injected.

When `mode=hmac`, the middleware rejects any non-exempt request that lacks a valid `X-Broker-Signature` header.

### Signature Format

Header: `X-Broker-Signature`

Value format:
```
t={unix_timestamp},v1={hex_encoded_hmac_sha256}
```

Example:
```
X-Broker-Signature: t=1708200000,v1=a1b2c3d4e5f6...
```

The signing payload is constructed as:
```
{timestamp}.{raw_request_body}
```

For requests without a body (e.g., GET), the signing payload is:
```
{timestamp}.
```

The signature is computed as:
```
HMAC-SHA256(signing_payload, base64_decode(shared_secret))
```

This format is intentionally similar to Stripe's webhook signature scheme because it is well-documented and many developers are already familiar with it.

### Middleware Behavior

The HMAC middleware wraps the HTTP handler chain in `controllers/router.go` at the `getHandler` function. In Go's middleware nesting, `hlog.AccessHandler` wraps the response writer, calls the inner handler, and then logs the response status. For it to capture HMAC rejection responses (401/403), it must be **outside** (wrapping around) the HMAC middleware:

```
Request → Logger → Request ID → Access Log (wraps response) → HMAC Middleware → Router → Controller
```

This ensures that rejected requests are recorded in the access log with their status code and request ID, which is important for security monitoring and incident response.

**Algorithm:**

1. Check if the request path matches any `auth-exempt-paths` prefix. If so, pass through.
2. Extract `X-Broker-Signature` header. If missing, return `401 Unauthorized`.
3. Parse the header into timestamp (`t`) and signature (`v1`). If malformed, return `401 Unauthorized`.
4. Parse `t` as a Unix timestamp. If not a valid integer, return `401 Unauthorized`.
5. Check `abs(now - t) <= hmac-timestamp-tolerance-seconds`. If outside window, return `403 Forbidden` with message "request timestamp outside tolerance window".
6. Read the full request body into a buffer. Replace `r.Body` with a new reader over the buffer so downstream handlers can read it again.
7. Construct signing payload: `{t}.{body}`.
8. Compute `HMAC-SHA256(signing_payload, decoded_secret)`.
9. Compare computed signature with `v1` using `crypto/hmac.Equal` (constant-time comparison to prevent timing attacks).
10. If mismatch, return `403 Forbidden` with message "invalid signature".
11. If match, call the next handler.

### Request Body Buffering

The broadcast controller already reads the full body via `io.ReadAll(r.Body)` (`controllers/broadcast.go:86`). The middleware must also read the body to compute the signature. To avoid double-reading:

The middleware reads the body, computes the signature, then replaces `r.Body` with `io.NopCloser(bytes.NewReader(bufferedBody))` so downstream handlers see the same body content. This is a standard Go pattern for middleware that inspects request bodies.

### Error Responses

| Condition | HTTP Status | Body |
|---|---|---|
| Missing `X-Broker-Signature` header | 401 | `missing X-Broker-Signature header` |
| Malformed signature header | 401 | `malformed X-Broker-Signature header` |
| Timestamp outside tolerance | 403 | `request timestamp outside tolerance window` |
| Signature mismatch | 403 | `invalid signature` |
| Body read error | 500 | `could not read request body` |

### Deployment with a Reverse Proxy / CDN

When deploying webhook-broker on the public internet, it should sit behind a reverse proxy or CDN (e.g., Cloudflare, AWS ALB, nginx) for DDoS protection and TLS termination:

```
Internet → Cloudflare/CDN (DDoS + TLS) → webhook-broker (HMAC + token auth)
```

**Recommended proxy-level protections:**

- **TLS termination**: The proxy handles HTTPS; webhook-broker listens on plain HTTP internally.
- **Rate limiting**: Configure rate limits at the proxy layer (e.g., Cloudflare rate limiting rules) to throttle abusive clients before they reach the broker.
- **IP restriction**: If the set of producing systems is known, restrict allowed source IPs at the proxy or firewall level.
- **Path blocking**: Block access to internal endpoints (`/metrics`, `/debug/pprof/*`, `/job-status`, `/dlq-status`) at the proxy level so they are not reachable from the public internet. These paths are auth-exempt by default so that internal infrastructure (Prometheus, profilers) can access them without HMAC signatures.
- **Origin verification**: Ensure the broker only accepts connections from the proxy (e.g., firewall rules restricting inbound connections to the proxy's IP ranges, or a shared header secret set by the proxy).

HMAC authentication is complementary to these protections. The proxy handles volumetric and network-level attacks; HMAC handles application-level authentication and payload integrity.

## Implementation Plan

### 1. Configuration (`config/`)

**New file: `config/gateway_auth_config.go`**

Define the `GatewayAuthConfig` interface and implement it on `*Config`:

```go
type GatewayAuthConfig interface {
    GetGatewayAuthMode() string           // "none" or "hmac"
    GetHMACSharedSecret() []byte          // base64-decoded secret
    GetHMACTimestampTolerance() time.Duration
    GetAuthExemptPaths() []string
    IsGatewayAuthEnabled() bool
}
```

**Modify: `config/config.go`**

- Add fields to `Config` struct: `GatewayAuthMode string`, `HMACSharedSecret string`, `HMACTimestampToleranceSeconds uint`, `AuthExemptPaths string`.
- Add `setupGatewayAuthConfiguration(cfg, configuration)` call in `GetConfigurationFromParseConfig`.
- Add validation: if `mode=hmac`, `hmac-shared-secret` must be non-empty and valid base64.

**Modify: `config/defaultconfig.go`**

Add default section:

```ini
[gateway-auth]
mode=none
hmac-shared-secret=
hmac-timestamp-tolerance-seconds=300
auth-exempt-paths=/_status,/metrics,/debug/pprof/
```

**Modify: `config/config.go` `ConfigInjector`**

Add `wire.Bind(new(GatewayAuthConfig), new(*Config))` to the wire set.

### 2. HMAC Middleware (`controllers/`)

**New file: `controllers/hmac_middleware.go`**

Implement the middleware function:

```go
func NewHMACMiddleware(authConfig config.GatewayAuthConfig) func(http.Handler) http.Handler
```

This returns a middleware that:
- No-ops if `authConfig.IsGatewayAuthEnabled()` is false
- Checks exempt paths
- Validates `X-Broker-Signature` header
- Verifies timestamp tolerance
- Reads body, computes HMAC, compares with constant-time equality
- Replaces `r.Body` for downstream consumption

**Modify: `controllers/router.go`**

- Update `getHandler` to accept `config.GatewayAuthConfig` and wrap the handler chain with the HMAC middleware.
- Update `ConfigureAPI` signature to accept `config.GatewayAuthConfig`.
- Update the `ControllerInjector` wire set if needed.

### 3. Wire Integration

**Modify: `main.go` and `wire.go`**

Ensure `GatewayAuthConfig` is available in the dependency graph. Since `*Config` already implements all config interfaces and `ConfigInjector` binds them, adding the new `wire.Bind` is sufficient.

Regenerate `wire_gen.go` after changes.

### 4. Tests

**New file: `controllers/hmac_middleware_test.go`**

Test cases:

- Mode `none`: requests pass through without signature check
- Mode `hmac`, no header: returns 401
- Mode `hmac`, malformed header: returns 401
- Mode `hmac`, valid signature: request passes through, body is readable downstream
- Mode `hmac`, wrong signature: returns 403
- Mode `hmac`, expired timestamp: returns 403
- Mode `hmac`, future timestamp beyond tolerance: returns 403
- Mode `hmac`, exempt path bypasses check
- Mode `hmac`, empty body (GET request): signature over timestamp only
- Constant-time comparison (verify `crypto/hmac.Equal` is used, not `==`)

**New file: `config/gateway_auth_config_test.go`**

Test cases:

- Default config: mode is `none`
- Config with `mode=hmac` and valid secret: parses correctly
- Config with `mode=hmac` and missing secret: validation error
- Config with `mode=hmac` and invalid base64 secret: validation error
- Timestamp tolerance defaults to 300 seconds
- Exempt paths parsing (single, multiple, empty)

### 5. Documentation

**Modify: `docs/configuration.md`**

Add the `[gateway-auth]` section documentation with all keys, defaults, and examples.

**Modify: `CLAUDE.md`**

Add a section documenting the gateway auth feature for AI agent context.

### 6. Integration Test

**Modify: `integration-test/main.go`**

The HMAC integration tests must run against a separate broker instance configured with `mode=hmac`, since enabling HMAC globally would break all existing integration tests (they don't sign requests). The approach:

1. Run the existing integration test suite against the default broker (no HMAC) as today
2. Start a second broker instance on a different port with a config that sets `mode=hmac` and a known secret
3. Run the HMAC-specific tests against this second instance:
   - Sends a properly signed broadcast request — expects 201
   - Sends a request with no signature — expects 401
   - Sends a request with wrong signature — expects 403
   - Sends a request with expired timestamp — expects 403
   - Sends a request to an exempt path (`/_status`) without signature — expects 200

## Client Examples

### Go

```go
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "strconv"
    "time"
)

func signRequest(body []byte, secret []byte) string {
    timestamp := strconv.FormatInt(time.Now().Unix(), 10)
    payload := timestamp + "." + string(body)
    mac := hmac.New(sha256.New, secret)
    mac.Write([]byte(payload))
    signature := hex.EncodeToString(mac.Sum(nil))
    return fmt.Sprintf("t=%s,v1=%s", timestamp, signature)
}
// Set header: req.Header.Set("X-Broker-Signature", signRequest(body, secret))
```

### Python

```python
import hmac, hashlib, time

def sign_request(body: bytes, secret: bytes) -> str:
    timestamp = str(int(time.time()))
    payload = f"{timestamp}.".encode() + body
    signature = hmac.new(secret, payload, hashlib.sha256).hexdigest()
    return f"t={timestamp},v1={signature}"
```

### Node.js / TypeScript

```typescript
import crypto from "crypto";

function signRequest(body: string, secret: Buffer): string {
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const payload = `${timestamp}.${body}`;
  const signature = crypto
    .createHmac("sha256", secret)
    .update(payload)
    .digest("hex");
  return `t=${timestamp},v1=${signature}`;
}
```

### curl (for testing)

```bash
# The secret must be base64-encoded in config. For this example we use a
# printable-ASCII secret so that shell argument passing works reliably.
# In production, use `openssl rand -base64 32` to generate a secret.
SECRET="dGVzdC1zZWNyZXQtZm9yLWhtYWM="   # base64("test-secret-for-hmac")
BODY='{"event": "order.created", "data": {"id": "123"}}'
TIMESTAMP=$(date +%s)

# Convert the secret from base64 to hex for openssl's -macopt hexkey,
# which safely handles arbitrary byte values (including non-printable).
HEX_KEY=$(echo -n "$SECRET" | base64 -d | xxd -p -c 256)
SIGNATURE=$(echo -n "${TIMESTAMP}.${BODY}" | openssl dgst -sha256 -mac hmac -macopt hexkey:"$HEX_KEY" | sed 's/.*= //')

curl -X POST http://localhost:8080/channel/my-channel/broadcast \
  -H "Content-Type: application/json" \
  -H "X-Broker-Channel-Token: my-channel-token" \
  -H "X-Broker-Producer-Token: my-producer-token" \
  -H "X-Broker-Producer-ID: my-producer" \
  -H "X-Broker-Signature: t=${TIMESTAMP},v1=${SIGNATURE}" \
  --data "$BODY"
```

## Security Considerations

- **Secret storage**: The shared secret is stored in the INI config file. In production, use environment variable substitution or a secrets manager to inject it rather than committing it to version control.
- **Secret rotation**: To rotate the secret, update the config and restart the broker. During rotation, there is a brief window where in-flight requests signed with the old secret will be rejected. For zero-downtime rotation, a future enhancement could support multiple active secrets.
- **Timing attacks**: The implementation must use `crypto/hmac.Equal` for signature comparison, not `==` or `bytes.Equal`, to prevent timing-based side-channel attacks.
- **Body size limits**: The middleware buffers the entire request body in memory. The existing `http.Server.ReadTimeout` and any proxy-level body size limits provide protection against oversized payloads. No additional limit is needed in the middleware since the broadcast controller already reads the full body.
