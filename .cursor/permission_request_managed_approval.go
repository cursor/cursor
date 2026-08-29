// Copyright (c) GitHub. All rights reserved.

package rpc

import "encoding/json"

func managedApprovalRequired(value *bool) bool {
	return value != nil && *value
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestCustomTool) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestExtensionManagement) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestExtensionPermissionAccess) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestFactory) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestHook) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestMCP) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestMemory) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestRead) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestShell) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestURL) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether managed policy requires an explicit
// human decision for this request.
func (r PermissionRequestWrite) RequiresManagedApproval() bool {
	return managedApprovalRequired(r.ManagedApprovalRequired)
}

// RequiresManagedApproval reports whether an unknown request carries managed
// approval metadata.
func (r RawPermissionRequest) RequiresManagedApproval() bool {
	var metadata struct {
		ManagedApprovalRequired *bool `json:"managedApprovalRequired"`
	}
	if json.Unmarshal(r.Raw, &metadata) != nil {
		return true
	}
	return managedApprovalRequired(metadata.ManagedApprovalRequired)
}
