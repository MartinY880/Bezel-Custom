// Package beszel provides core application constants and version information
// which are used throughout the application.
package beszel

import "github.com/blang/semver"

const (
	// Version is the current version of the application (fork numbering).
	// Bump the patch on every release that changes the agent, so pushed agents
	// are distinguishable in the UI. IMPORTANT: must always sort ABOVE 0.18.3
	// in semver order — the hub's Min* checks (0.12/0.13) and the frontend
	// feature gates (0.14/0.15/0.16) compare this value, so a lower version
	// (e.g. 0.5.x) would silently disable features and legacy-downgrade the
	// protocol.
	Version = "5.24.4"
	// AppName is the name of the application.
	AppName = "beszel"
)

// MinVersionCbor is the minimum supported version for CBOR compatibility.
var MinVersionCbor = semver.MustParse("0.12.0")

// MinVersionAgentResponse is the minimum supported version for AgentResponse compatibility.
var MinVersionAgentResponse = semver.MustParse("0.13.0")
