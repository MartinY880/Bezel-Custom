package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/henrygd/beszel/internal/common"
	"github.com/henrygd/beszel/internal/entities/smart"

	"log/slog"
)

// HandlerContext provides context for request handlers
type HandlerContext struct {
	Client      *WebSocketClient
	Agent       *Agent
	Request     *common.HubRequest[cbor.RawMessage]
	RequestID   *uint32
	HubVerified bool
	// SendResponse abstracts how a handler sends responses (WS or SSH)
	SendResponse func(data any, requestID *uint32) error
}

// RequestHandler defines the interface for handling specific websocket request types
type RequestHandler interface {
	// Handle processes the request and returns an error if unsuccessful
	Handle(hctx *HandlerContext) error
}

// Responder sends handler responses back to the hub (over WS or SSH)
type Responder interface {
	SendResponse(data any, requestID *uint32) error
}

// HandlerRegistry manages the mapping between actions and their handlers
type HandlerRegistry struct {
	handlers map[common.WebSocketAction]RequestHandler
}

// NewHandlerRegistry creates a new handler registry with default handlers
func NewHandlerRegistry() *HandlerRegistry {
	registry := &HandlerRegistry{
		handlers: make(map[common.WebSocketAction]RequestHandler),
	}

	registry.Register(common.GetData, &GetDataHandler{})
	registry.Register(common.CheckFingerprint, &CheckFingerprintHandler{})
	registry.Register(common.GetContainerLogs, &GetContainerLogsHandler{})
	registry.Register(common.GetContainerInfo, &GetContainerInfoHandler{})
	registry.Register(common.GetSmartData, &GetSmartDataHandler{})
	registry.Register(common.GetSystemdInfo, &GetSystemdInfoHandler{})
	registry.Register(common.GetPackageUpdates, &GetPackageUpdatesHandler{})
	registry.Register(common.ApplyPackageUpdates, &ApplyPackageUpdatesHandler{})
	registry.Register(common.GetPackageUpdateStatus, &GetPackageUpdateStatusHandler{})
	registry.Register(common.UpdateAgent, &UpdateAgentHandler{})

	return registry
}

// Register registers a handler for a specific action type
func (hr *HandlerRegistry) Register(action common.WebSocketAction, handler RequestHandler) {
	hr.handlers[action] = handler
}

// Handle routes the request to the appropriate handler
func (hr *HandlerRegistry) Handle(hctx *HandlerContext) error {
	handler, exists := hr.handlers[hctx.Request.Action]
	if !exists {
		return fmt.Errorf("unknown action: %d", hctx.Request.Action)
	}

	// Check verification requirement - default to requiring verification
	if hctx.Request.Action != common.CheckFingerprint && !hctx.HubVerified {
		return errors.New("hub not verified")
	}

	// Log handler execution for debugging
	// slog.Debug("Executing handler", "action", hctx.Request.Action)

	return handler.Handle(hctx)
}

// GetHandler returns the handler for a specific action
func (hr *HandlerRegistry) GetHandler(action common.WebSocketAction) (RequestHandler, bool) {
	handler, exists := hr.handlers[action]
	return handler, exists
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetDataHandler handles system data requests
type GetDataHandler struct{}

func (h *GetDataHandler) Handle(hctx *HandlerContext) error {
	var options common.DataRequestOptions
	_ = cbor.Unmarshal(hctx.Request.Data, &options)

	sysStats := hctx.Agent.gatherStats(options)
	return hctx.SendResponse(sysStats, hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// CheckFingerprintHandler handles authentication challenges
type CheckFingerprintHandler struct{}

func (h *CheckFingerprintHandler) Handle(hctx *HandlerContext) error {
	return hctx.Client.handleAuthChallenge(hctx.Request, hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetContainerLogsHandler handles container log requests
type GetContainerLogsHandler struct{}

func (h *GetContainerLogsHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.dockerManager == nil {
		return hctx.SendResponse("", hctx.RequestID)
	}

	var req common.ContainerLogsRequest
	if err := cbor.Unmarshal(hctx.Request.Data, &req); err != nil {
		return err
	}

	ctx := context.Background()
	logContent, err := hctx.Agent.dockerManager.getLogs(ctx, req.ContainerID)
	if err != nil {
		return err
	}

	return hctx.SendResponse(logContent, hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetContainerInfoHandler handles container info requests
type GetContainerInfoHandler struct{}

func (h *GetContainerInfoHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.dockerManager == nil {
		return hctx.SendResponse("", hctx.RequestID)
	}

	var req common.ContainerInfoRequest
	if err := cbor.Unmarshal(hctx.Request.Data, &req); err != nil {
		return err
	}

	ctx := context.Background()
	info, err := hctx.Agent.dockerManager.getContainerInfo(ctx, req.ContainerID)
	if err != nil {
		return err
	}

	return hctx.SendResponse(string(info), hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetSmartDataHandler handles SMART data requests
type GetSmartDataHandler struct{}

func (h *GetSmartDataHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.smartManager == nil {
		// return empty map to indicate no data
		return hctx.SendResponse(map[string]smart.SmartData{}, hctx.RequestID)
	}
	if err := hctx.Agent.smartManager.Refresh(false); err != nil {
		slog.Debug("smart refresh failed", "err", err)
	}
	data := hctx.Agent.smartManager.GetCurrentData()
	return hctx.SendResponse(data, hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetSystemdInfoHandler handles detailed systemd service info requests
type GetSystemdInfoHandler struct{}

func (h *GetSystemdInfoHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.systemdManager == nil {
		return errors.ErrUnsupported
	}

	var req common.SystemdInfoRequest
	if err := cbor.Unmarshal(hctx.Request.Data, &req); err != nil {
		return err
	}
	if req.ServiceName == "" {
		return errors.New("service name is required")
	}

	details, err := hctx.Agent.systemdManager.getServiceDetails(req.ServiceName)
	if err != nil {
		return err
	}

	return hctx.SendResponse(details, hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetPackageUpdatesHandler returns the cached list of available package updates
type GetPackageUpdatesHandler struct{}

func (h *GetPackageUpdatesHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.pkgUpdateManager == nil {
		return errors.ErrUnsupported
	}

	var req common.PackageUpdatesRequest
	_ = cbor.Unmarshal(hctx.Request.Data, &req)

	if req.Refresh {
		if err := hctx.Agent.pkgUpdateManager.refresh(); err != nil {
			return err
		}
	}
	return hctx.SendResponse(hctx.Agent.pkgUpdateManager.getUpdates(), hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// ApplyPackageUpdatesHandler starts a background install of the selected
// packages and responds immediately with the job status. Poll with
// GetPackageUpdateStatus for the outcome.
type ApplyPackageUpdatesHandler struct{}

func (h *ApplyPackageUpdatesHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.pkgUpdateManager == nil {
		return errors.ErrUnsupported
	}

	var req common.ApplyPackageUpdatesRequest
	if err := cbor.Unmarshal(hctx.Request.Data, &req); err != nil {
		return err
	}
	if len(req.Packages) == 0 {
		return errors.New("no packages specified")
	}

	status, err := hctx.Agent.pkgUpdateManager.startApply(req.Packages)
	if err != nil {
		return err
	}
	return hctx.SendResponse(status, hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// GetPackageUpdateStatusHandler reports the current/last background apply job
type GetPackageUpdateStatusHandler struct{}

func (h *GetPackageUpdateStatusHandler) Handle(hctx *HandlerContext) error {
	if hctx.Agent.pkgUpdateManager == nil {
		return errors.ErrUnsupported
	}
	return hctx.SendResponse(hctx.Agent.pkgUpdateManager.applyStatus(), hctx.RequestID)
}

////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////

// UpdateAgentHandler downloads the fork binary from the hub, swaps it over
// the running executable, and restarts the agent
type UpdateAgentHandler struct{}

func (h *UpdateAgentHandler) Handle(hctx *HandlerContext) error {
	var req common.AgentUpdateRequest
	if err := cbor.Unmarshal(hctx.Request.Data, &req); err != nil {
		return err
	}
	result, err := selfUpdateFromHub(req)
	if err != nil {
		return err
	}
	return hctx.SendResponse(result, hctx.RequestID)
}
