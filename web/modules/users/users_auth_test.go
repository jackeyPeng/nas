package users

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nas-panel/common"
)

func TestIsSelfOrAdminPasswordChange(t *testing.T) {
	tests := []struct {
		name      string
		requester string
		nasUser   string
		target    string
		want      bool
	}{
		{"admin_changes_self", "fm", "fm", "fm", true},
		{"admin_changes_other", "fm", "fm", "bob", true},
		{"user_changes_self", "alice", "fm", "alice", true},
		{"user_changes_other", "alice", "fm", "bob", false},
		{"anonymous_denied", "", "fm", "alice", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSelfOrAdminPasswordChange(tt.requester, tt.nasUser, tt.target); got != tt.want {
				t.Errorf("isSelfOrAdminPasswordChange() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleUserAction_NonAdminCannotChangeOtherPassword(t *testing.T) {
	common.InitAuth("users-auth-test-secret")
	token, _ := common.CreateToken("alice")
	common.SetNasPass("not-default")
	t.Cleanup(func() { common.SetNasPass("") })

	req := httptest.NewRequest(http.MethodPut, "/api/users/bob/password", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handleUserAction(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin changing other user password should be 403, got %d", rec.Code)
	}
}

func TestHandleUserAction_NonAdminCannotChangeServices(t *testing.T) {
	common.InitAuth("users-auth-test-secret")
	token, _ := common.CreateToken("alice")
	common.SetNasPass("not-default")
	t.Cleanup(func() { common.SetNasPass("") })

	req := httptest.NewRequest(http.MethodPut, "/api/users/bob/services", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handleUserAction(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin service toggle should be 403, got %d", rec.Code)
	}
}
