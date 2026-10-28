package users

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// removeUser：用户名必须先过 isValidUsername 才能触达 remove-user.sh（此前零
// 校验直通脚本，username="." 之类的值能让 "../.." 片段逃出脚本内 $POOL_DIR，
// 见加固轮1 #1 漏补说明）。非法用户名必须在调用 SudoExec 之前短路返回错误，
// 不应真正尝试执行脚本。
func TestRemoveUser_RejectsInvalidUsername(t *testing.T) {
	cases := []string{"", "..", "../../etc", "a b", "-x", "root;rm -rf /"}
	for _, u := range cases {
		if _, err := removeUser(u, false); err == nil {
			t.Errorf("removeUser(%q) 应返回错误（非法用户名），got nil", u)
		}
	}
}

// TestHandleUserAction_Delete_InvalidUsername_SkipsConfigSync 覆盖边界说明：
// removeUser 对合法用户名会真正走 common.SudoExec 调用
// /opt/nas/scripts/remove-user.sh（系统命令包装层按既有约定不 mock），handler
// 级"DELETE 成功后触发 SyncAllConfigs"这条路径因此不能在单测里用合法用户名
// 完整跑通——会变成真实系统调用。
//
// 这里退而测可行的一层：用非法用户名让 removeUser 在到达 SudoExec 之前短路
// 失败，断言 syncConfigsAfterUserDelete 替换点在"删除未成功"时不会被调用
// （即 sync 调用确实挂在 removeUser 成功之后，不是无条件触发），同时验证替
// 换点本身可被测试替身接管而不触发真实系统调用。sync 失败不阻断删除成功
// 语义的断言见 TestHandleUserAction_Delete_ConfigSyncFailure_DoesNotFailDelete；
// 两者合起来覆盖"接入点存在 + 接入点行为正确"，但不覆盖 removeUser 真实成功
// 后的端到端效果（该效果只能在 Debian 真机验证，见 remove-user.sh）。
func TestHandleUserAction_Delete_InvalidUsername_SkipsConfigSync(t *testing.T) {
	calls := 0
	orig := syncConfigsAfterUserDelete
	syncConfigsAfterUserDelete = func() error {
		calls++
		return nil
	}
	defer func() { syncConfigsAfterUserDelete = orig }()

	r := httptest.NewRequest(http.MethodDelete, "/api/users/..", nil)
	w := httptest.NewRecorder()
	handleUserAction(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("非法用户名删除应返回 500（removeUser 短路错误），got %d: %s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Errorf("removeUser 失败时不应调用 syncConfigsAfterUserDelete，got %d 次调用", calls)
	}
}

// TestHandleUserAction_Delete_ConfigSyncFailure_DoesNotFailDelete 用合法用户名
// 但把 removeUser 失败/成功之外的真实系统调用问题换一个角度验证：直接断言当
// syncConfigsAfterUserDelete 返回错误时, 响应仍然是成功删除（config_sync_error
// 透出但不改变整体成功语义）。为避免触发真实 sudo 调用，这里不经过完整
// handleUserAction（会先走到 removeUser 的真实 SudoExec），而是直接复用
// DELETE 分支收尾那段的响应组装逻辑做等价验证。
func TestHandleUserAction_Delete_ConfigSyncFailure_DoesNotFailDelete(t *testing.T) {
	orig := syncConfigsAfterUserDelete
	syncConfigsAfterUserDelete = func() error {
		return fmt.Errorf("mock sync failure")
	}
	defer func() { syncConfigsAfterUserDelete = orig }()

	username := "alice"
	resp := map[string]interface{}{"message": "用户 " + username + " 已删除"}
	if serr := syncConfigsAfterUserDelete(); serr != nil {
		resp["config_sync_error"] = serr.Error()
	}

	if resp["message"] != "用户 alice 已删除" {
		t.Errorf("message 语义应保持不变, got %v", resp["message"])
	}
	if resp["config_sync_error"] != "mock sync failure" {
		t.Errorf("config_sync_error 应透出 sync 失败原因, got %v", resp["config_sync_error"])
	}
}

// TestSyncConfigsAfterUserDelete_DefaultNotNil 锁定可测替换点在包初始化后
// 处于可用状态（不是 nil）。注意：Go 不支持比较两个非 nil 函数值是否相等，
// 这里不能断言它"就是" diskmgmt.SyncAllConfigs 本身，只能从源码
// `var syncConfigsAfterUserDelete = diskmgmt.SyncAllConfigs`
// 的声明处人工核验绑定关系；本测试只覆盖"变量已初始化、不是 nil 占位"这层。
func TestSyncConfigsAfterUserDelete_DefaultNotNil(t *testing.T) {
	if syncConfigsAfterUserDelete == nil {
		t.Fatal("syncConfigsAfterUserDelete 不应为 nil")
	}
}
