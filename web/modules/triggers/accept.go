// accept.go 实现建议接受链（F2 Task 5）：建议→plan 固化→pending action。
// 执行确认仍然只有一条既有路径（POST /api/ai/action/confirm →
// executeConfirmedAction → tasks.StartJob），本文件不新增任何确认语义，
// 只负责把一张 state=new 的 task_plan 建议卡，转成一张可以拿去走既有
// 确认门的 PendingAction（硬约束：零新确认语义红线）。
package triggers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nas-panel/common"
	"nas-panel/modules/ai"
	"nas-panel/modules/tasks"
)

// acceptPlanBudget 是 accept 链内 PlanPhotoDedupe 同步扫描的预算上限，
// 与 ai/tools_tasks.go 的 taskPlanBudget 取值一致（接受建议本质上就是
// 代用户点了一次"生成计划"，预算语义理应相同）。
const acceptPlanBudget = 120 * time.Second

// AcceptResult 是 AcceptSuggestion 的产出：前端拿着它直接开确认弹窗，
// 弹窗提交走既有 POST /api/ai/action/confirm（不是本文件的端点）。
type AcceptResult struct {
	ActionID    string `json:"action_id"`
	ConfirmWord string `json:"confirm_word"`
	Summary     string `json:"summary"`
	JobID       string `json:"job_id"`
}

// acceptAction 是 actions_json 数组里取出的第一项的最小反序列化形状，
// 接受链只关心 type/kind/scope 三个字段（navigate 类的 page 字段不在
// accept 链的消费范围内——navigate 卡根本不走 accept）。
type acceptAction struct {
	Type  string `json:"type"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
}

// AcceptSuggestion 把一张 state=new 的建议卡固化成一个任务计划 +
// 待确认操作卡：
//  1. GetSuggestion：不存在 → error（REST 层映射 404）
//  2. state≠new → error（REST 层映射 409，重放/dismissed/expired 均在此拦）
//  3. 解析 ActionsJSON 取第一项：非 task_plan（如 navigate）→ 明确错误，
//     navigate 类建议只能前端直接跳转+dismiss，不调这个函数
//  4. kind≠photo_dedupe → 明确错误（F2 只支持这一种，F4 扩）
//  5. tasks.PlanPhotoDedupe(scope, 120s) 生成/复用计划
//  6. ai.StartTaskSummary(job) 构造摘要（与模型主动 start_task 时的确认卡
//     摘要同源）；0 组时追加"没有发现重复文件"提示，但仍正常往下走——
//     PlanPhotoDedupe 对 0 组候选照样建一个 0 步骤 pending_confirm job
//     （F1 CreateJob 不拒绝空 plan，读代码确认的真实语义），不是一条
//     独立的"拒绝"分支
//  7. ai.CreatePendingAction("start_task", {"job_id":...}, summary)
//  8. 建议 state→accepted + 审计
//
// 失败路径（步骤 2/3/4/5/7 任一出错）不改变建议 state——保持 new，
// 用户可以重试或改用别的入口。
func AcceptSuggestion(id string) (AcceptResult, error) {
	sg, ok := GetSuggestion(id)
	if !ok {
		return AcceptResult{}, fmt.Errorf("建议不存在: %s", id)
	}
	if sg.State != StateNew {
		return AcceptResult{}, fmt.Errorf("非法状态迁移: 建议当前状态 %s 不可接受", sg.State)
	}

	action, err := firstTaskPlanAction(sg.ActionsJSON)
	if err != nil {
		return AcceptResult{}, err
	}
	if action.Kind != "photo_dedupe" {
		return AcceptResult{}, fmt.Errorf("暂不支持的任务类型: %s", action.Kind)
	}

	job, partial, err := tasks.PlanPhotoDedupe(action.Scope, acceptPlanBudget)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("生成任务计划失败: %w", err)
	}

	summary := ai.StartTaskSummary(job)
	if job.Impact.Groups == 0 {
		summary = summary + "（没有发现重复文件，可以直接确认完成，不会移动任何文件）"
	} else if partial {
		summary = summary + "（部分扫描：预算内未扫完全部文件，已生成部分计划）"
	}

	argsJSON, err := json.Marshal(map[string]string{"job_id": job.ID})
	if err != nil {
		return AcceptResult{}, fmt.Errorf("序列化 start_task 参数失败: %w", err)
	}

	actionID, confirmWord, err := ai.CreatePendingAction("start_task", string(argsJSON), summary)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("生成待确认操作卡失败: %w", err)
	}

	if err := markSuggestionAccepted(sg.ID); err != nil {
		return AcceptResult{}, fmt.Errorf("建议状态更新失败: %w", err)
	}

	return AcceptResult{
		ActionID:    actionID,
		ConfirmWord: confirmWord,
		Summary:     summary,
		JobID:       job.ID,
	}, nil
}

// firstTaskPlanAction 解析 actions_json，取第一项并要求 type=="task_plan"。
// navigate-only 卡（type=="navigate"）在这里被明确拒绝——计划红线：
// navigate 类建议前端直接跳转+dismiss，不调 accept。
func firstTaskPlanAction(actionsJSON string) (acceptAction, error) {
	var actions []acceptAction
	if err := json.Unmarshal([]byte(actionsJSON), &actions); err != nil {
		return acceptAction{}, fmt.Errorf("建议的 actions_json 解析失败: %w", err)
	}
	if len(actions) == 0 {
		return acceptAction{}, fmt.Errorf("建议没有可执行的动作")
	}
	first := actions[0]
	if first.Type != "task_plan" {
		return acceptAction{}, fmt.Errorf("该建议是 %s 类，不支持接受——请直接跳转查看", first.Type)
	}
	return first, nil
}

// markSuggestionAccepted 把 state=new 的建议迁到 accepted（AcceptSuggestion
// 走完全部前置校验、拿到 PendingAction 之后才调用，失败路径永远不会
// 碰到这个函数——state 只在整条链成功后才变化）。
func markSuggestionAccepted(id string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return fmt.Errorf("triggers: 未初始化")
	}
	_, err := db.Exec(`UPDATE suggestions SET state = ? WHERE id = ? AND state = ?`, StateAccepted, id, StateNew)
	return err
}

// handleAcceptSuggestion: POST /api/triggers/suggestions/{id}/accept →
// 200 {"action_id","confirm_word","summary","job_id"}。前端拿它开确认
// 弹窗，提交走既有 POST /api/ai/action/confirm（零新确认语义）。
func handleAcceptSuggestion(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := AcceptSuggestion(id)
	if err != nil {
		status := http.StatusBadRequest
		msg := err.Error()
		if strings.Contains(msg, "不存在") {
			status = http.StatusNotFound
		} else if strings.Contains(msg, "非法状态迁移") {
			status = http.StatusConflict
		}
		common.LogAuditRequest(r, "TRIGGERS", "接受建议", "suggestion_id="+id+" error="+msg, "failed")
		jsonError(w, msg, status)
		return
	}
	common.LogAuditRequest(r, "TRIGGERS", "接受建议",
		fmt.Sprintf("suggestion_id=%s job_id=%s action_id=%s", id, result.JobID, result.ActionID), "success")
	common.JSONResponse(w, map[string]interface{}{
		"action_id":    result.ActionID,
		"confirm_word": result.ConfirmWord,
		"summary":      result.Summary,
		"job_id":       result.JobID,
	})
}
