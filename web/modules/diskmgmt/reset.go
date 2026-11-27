package diskmgmt

import (
	"net/http"

	"nas-panel/common"
)

// resetDisabledMessage is returned by both the non-streaming and streaming
// reset entry points. It must be returned before any destructive command
// (umount/lvremove/vgremove/mdadm/wipefs/fstab write/mkdir/chown) is issued.
//
// 本轮修复（release-blocking）：此前 handleWizardReset 的 fail-closed 只覆盖
// "pvs/mdstat 采集失败"这一种情况——采集成功时，managedTargets 仍然只是
// collectManagedWipeTargets 对 pvs/mdstat 的一次实时全量扫描结果，不是任何
// "这些设备只属于本面板纳管池、不属于宿主机上其它用途"的证明。换句话说，
// 只要宿主机上还有别的 LVM/RAID 使用者，重置功能依然可能拆除/擦除不属于本
// 面板的设备——此前 7 轮审计把这个结构性问题当成"可接受的残余风险、已用
// 交集/对齐手段充分缓解"，但缓解不等于证明。产品决定在给出真正的设备归属
// 证明（例如面板自建时持久化记录的设备身份）之前，整个重置功能必须完全
// 禁用，不能再执行任何拆除/擦盘动作——哪怕只是"看起来安全"的那部分。
const resetDisabledMessage = "重置功能暂时禁用，无法证明只操作本面板纳管池"

// handleWizardReset: fail-closed stub. The reset feature is temporarily
// disabled because the current implementation cannot prove that the
// devices it would touch belong exclusively to this panel's managed pool
// (see resetDisabledMessage). This handler returns an error immediately,
// before touching confirm/confirm_token, before any sudoOutputFn/sudoExecFn
// call, and before any filesystem/LVM/RAID/disk operation.
func handleWizardReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	common.LogAuditRequest(r, "STORAGE", "存储重置", "重置功能已禁用，请求被拒绝", "rejected")
	jsonError(w, resetDisabledMessage, http.StatusServiceUnavailable)
}
