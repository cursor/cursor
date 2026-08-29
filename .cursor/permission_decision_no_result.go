// Copyright (c) GitHub. All rights reserved.

package rpc

import "encoding/json"

// PermissionDecisionNoResult is an SDK-only [PermissionDecision] value
// returned by a permission handler when it declines to respond to a
// request, allowing another connected client to answer instead. The SDK
// suppresses the response on the wire when it sees this variant.
type PermissionDecisionNoResult struct{}

func (PermissionDecisionNoResult) permissionDecision() {}
func (PermissionDecisionNoResult) Kind() PermissionDecisionKind {
	return PermissionDecisionKind("no-result")
}

// MarshalJSON emits {"kind":"no-result"} for serialization symmetry with
// the other PermissionDecision variants. The SDK normally suppresses this
// value before it reaches the wire, but a stable representation is useful
// for tests and logging.
func (PermissionDecisionNoResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind string `json:"kind"`
	}{Kind: "no-result"})
}
