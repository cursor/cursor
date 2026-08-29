package copilot_test

import (
	"encoding/json"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestPermissionEventExposesManagedApprovalRequired(t *testing.T) {
	var data copilot.PermissionRequestedData
	err := json.Unmarshal([]byte(`{
		"permissionRequest": {
			"kind": "read",
			"intention": "Read managed content",
			"path": "/workspace/file.txt",
			"managedApprovalRequired": true
		},
		"requestId": "permission-1"
	}`), &data)
	if err != nil {
		t.Fatal(err)
	}

	if !data.PermissionRequest.RequiresManagedApproval() {
		t.Fatal("expected managed approval to be required")
	}
}

func TestApproveAllApprovesOrdinaryRequest(t *testing.T) {
	decision, err := copilot.PermissionHandler.ApproveAll(
		&copilot.PermissionRequestRead{},
		copilot.PermissionInvocation{SessionID: "session-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decision.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Fatalf("expected PermissionDecisionApproveOnce, got %T", decision)
	}
}

func TestApproveAllRejectsManagedSettingsSession(t *testing.T) {
	decision, err := copilot.PermissionHandler.ApproveAll(
		&copilot.PermissionRequestRead{},
		copilot.PermissionInvocation{
			SessionID:              "session-1",
			ManagedSettingsEnabled: true,
		},
	)
	if err == nil {
		t.Fatal("expected managed settings error")
	}
	if decision != nil {
		t.Fatalf("expected no decision, got %T", decision)
	}
}

func TestApproveAllLeavesManagedRequestPending(t *testing.T) {
	decision, err := copilot.PermissionHandler.ApproveAll(
		&copilot.PermissionRequestRead{ManagedApprovalRequired: ptrTo(true)},
		copilot.PermissionInvocation{SessionID: "session-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decision.(*rpc.PermissionDecisionNoResult); !ok {
		t.Fatalf("expected PermissionDecisionNoResult, got %T", decision)
	}
}

func TestRawPermissionRequestWithMalformedJSONRequiresManagedApproval(t *testing.T) {
	request := rpc.RawPermissionRequest{Raw: json.RawMessage(`{"managedApprovalRequired":`)}
	if !request.RequiresManagedApproval() {
		t.Fatal("expected malformed raw request to fail closed")
	}
}

func ptrTo[T any](value T) *T {
	return &value
}
