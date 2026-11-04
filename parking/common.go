// Package parking drains parked jobs (jobs of consumers that were paused) back into the job queue once their
// consumer is no longer paused, and hands them to the cluster through the load-balanced broker endpoint.
package parking

import (
	"github.com/google/wire"
)

const (
	// InternalEnqueuePath is the internal endpoint a node exposes for receiving handed-off job ids
	InternalEnqueuePath = "/_internal/jobs/enqueue"
	// AdminTokenHeader carries the shared [dispatch-pause] admin-token
	AdminTokenHeader = "X-Broker-Admin-Token"
	// SignatureHeader carries the HMAC signature when gateway auth is enabled (same format as the gateway middleware)
	SignatureHeader = "X-Broker-Signature"
)

const (
	panicString = "parameters null"
)

// EnqueueRequest is the body of a hand-off request: ids of jobs that are QUEUED in the job table
type EnqueueRequest struct {
	JobIDs []string `json:"jobIds"`
}

// EnqueueResponse is the body of a successful hand-off response
type EnqueueResponse struct {
	Accepted int `json:"accepted"`
}

var (
	// DrainerInjector is the injector for the parking drainer
	DrainerInjector = wire.NewSet(NewDrainerConfiguration, NewDrainer)
)

// Generated with assistance from Claude AI
