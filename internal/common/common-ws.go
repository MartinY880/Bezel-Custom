package common

import (
	"github.com/fxamacker/cbor/v2"
	"github.com/henrygd/beszel/internal/entities/smart"
	"github.com/henrygd/beszel/internal/entities/system"
	"github.com/henrygd/beszel/internal/entities/systemd"
)

type WebSocketAction = uint8

const (
	// Request system data from agent
	GetData WebSocketAction = iota
	// Check the fingerprint of the agent
	CheckFingerprint
	// Request container logs from agent
	GetContainerLogs
	// Request container info from agent
	GetContainerInfo
	// Request SMART data from agent
	GetSmartData
	// Request detailed systemd service info from agent
	GetSystemdInfo
	// Request list of available OS package updates from agent
	GetPackageUpdates
	// Install selected OS package updates on agent
	ApplyPackageUpdates
	// Request status of a background package-update apply job
	GetPackageUpdateStatus
	// Tell the agent to download the fork binary from the hub and self-update
	UpdateAgent
	// Tell the agent to reboot the host (used when a reboot is required to
	// finish applying updates)
	RebootSystem
	// Add new actions here...
)

// HubRequest defines the structure for requests sent from hub to agent.
type HubRequest[T any] struct {
	Action WebSocketAction `cbor:"0,keyasint"`
	Data   T               `cbor:"1,keyasint,omitempty,omitzero"`
	Id     *uint32         `cbor:"2,keyasint,omitempty"`
}

// AgentResponse defines the structure for responses sent from agent to hub.
type AgentResponse struct {
	Id          *uint32                    `cbor:"0,keyasint,omitempty"`
	SystemData  *system.CombinedData       `cbor:"1,keyasint,omitempty,omitzero"` // Legacy (<= 0.17)
	Fingerprint *FingerprintResponse       `cbor:"2,keyasint,omitempty,omitzero"` // Legacy (<= 0.17)
	Error       string                     `cbor:"3,keyasint,omitempty,omitzero"`
	String      *string                    `cbor:"4,keyasint,omitempty,omitzero"` // Legacy (<= 0.17)
	SmartData   map[string]smart.SmartData `cbor:"5,keyasint,omitempty,omitzero"` // Legacy (<= 0.17)
	ServiceInfo systemd.ServiceDetails     `cbor:"6,keyasint,omitempty,omitzero"` // Legacy (<= 0.17)
	// Data is the generic response payload for new endpoints (0.18+)
	Data cbor.RawMessage `cbor:"7,keyasint,omitempty,omitzero"`
}

type FingerprintRequest struct {
	Signature   []byte `cbor:"0,keyasint"`
	NeedSysInfo bool   `cbor:"1,keyasint"` // For universal token system creation
}

type FingerprintResponse struct {
	Fingerprint string `cbor:"0,keyasint"`
	// Optional system info for universal token system creation
	Hostname string `cbor:"1,keyasint,omitzero"`
	Port     string `cbor:"2,keyasint,omitzero"`
	Name     string `cbor:"3,keyasint,omitzero"`
}

type DataRequestOptions struct {
	CacheTimeMs    uint16 `cbor:"0,keyasint"`
	IncludeDetails bool   `cbor:"1,keyasint"`
}

type ContainerLogsRequest struct {
	ContainerID string `cbor:"0,keyasint"`
}

type ContainerInfoRequest struct {
	ContainerID string `cbor:"0,keyasint"`
}

type SystemdInfoRequest struct {
	ServiceName string `cbor:"0,keyasint"`
}

type PackageUpdatesRequest struct {
	Refresh bool `cbor:"0,keyasint,omitempty"` // force a fresh check instead of returning cached results
}

type ApplyPackageUpdatesRequest struct {
	Packages []string `cbor:"0,keyasint"`
	// All applies every cached non-held update (Packages is ignored), so the
	// hub can trigger "update everything" without shipping package lists.
	All bool `cbor:"1,keyasint,omitempty"`
	// SecurityOnly narrows All to security updates.
	SecurityOnly bool `cbor:"2,keyasint,omitempty"`
}

// Package apply job states reported in PackageApplyStatus.Status
const (
	PkgApplyIdle    = "idle"    // no apply has run since agent start
	PkgApplyRunning = "running" // install in progress
	PkgApplyDone    = "done"    // last apply finished successfully
	PkgApplyFailed  = "failed"  // last apply exited non-zero or was interrupted
)

// AgentUpdateRequest tells an agent to replace its binary with the one the
// hub serves at URL/agent-download?arch=<GOARCH> and restart. Checksums maps
// arch -> sha256 hex of the staged binary, so the agent can verify the
// download and skip the swap when already up to date.
type AgentUpdateRequest struct {
	URL       string            `cbor:"0,keyasint"`
	Checksums map[string]string `cbor:"1,keyasint"`
}

// AgentUpdateResponse reports the outcome of an UpdateAgent request.
type AgentUpdateResponse struct {
	Updated bool   `json:"updated" cbor:"0,keyasint"`
	Message string `json:"message,omitempty" cbor:"1,keyasint,omitempty"`
}

// PackageApplyStatus describes the state of a background package-update apply.
// ApplyPackageUpdates responds with this immediately (Status=running) and
// GetPackageUpdateStatus returns the current/last job state.
type PackageApplyStatus struct {
	Status     string   `json:"status" cbor:"0,keyasint"`
	Packages   []string `json:"packages,omitempty" cbor:"1,keyasint,omitempty"`
	Message    string   `json:"message,omitempty" cbor:"2,keyasint,omitempty"` // error detail / log tail on failure
	StartedAt  int64    `json:"startedAt,omitempty" cbor:"3,keyasint,omitempty"`
	FinishedAt int64    `json:"finishedAt,omitempty" cbor:"4,keyasint,omitempty"`
}
