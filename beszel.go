// Package beszel provides core application constants and version information
// which are used throughout the application.
package beszel

import "github.com/blang/semver"

const (
	// Version is the current version of the application.
	// The -fork.N suffix identifies fork builds: bump N on every release that
	// changes the agent, so pushed agents are distinguishable in the UI.
	// (Pre-release suffixes parse fine everywhere: blang/semver on the backend,
	// and the frontend's parseSemVer strips them before comparing, so the
	// 0.14/0.15/0.16 feature gates and Min* version checks are unaffected.)
	Version = "0.18.3-fork.3"
	// AppName is the name of the application.
	AppName = "beszel"
)

// MinVersionCbor is the minimum supported version for CBOR compatibility.
var MinVersionCbor = semver.MustParse("0.12.0")

// MinVersionAgentResponse is the minimum supported version for AgentResponse compatibility.
var MinVersionAgentResponse = semver.MustParse("0.13.0")
