package copilot

import (
	"errors"

	"github.com/github/copilot-sdk/go/rpc"
)

// PermissionHandler provides pre-built OnPermissionRequest implementations.
var PermissionHandler = struct {
	// ApproveAll approves permission requests when managed settings are disabled.
	ApproveAll PermissionHandlerFunc
}{
	ApproveAll: func(request PermissionRequest, invocation PermissionInvocation) (rpc.PermissionDecision, error) {
		if invocation.ManagedSettingsEnabled {
			return nil, errors.New("approveAll cannot be used when managed settings are enabled")
		}
		if request.RequiresManagedApproval() {
			return &rpc.PermissionDecisionNoResult{}, nil
		}
		return &rpc.PermissionDecisionApproveOnce{}, nil
	},
}
