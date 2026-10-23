// export.go 是 ai 模块对外的稳定导出面（F2 Task 5）：triggers 的建议
// 接受链复用这里导出的 CreatePendingAction/StartTaskSummary，不另起
// 一套确认门/摘要逻辑——零新确认语义红线：执行确认仍然只有一条路径
// （POST /api/ai/action/confirm → executeConfirmedAction），本文件只是
// 把"生产一张待确认操作卡"这一步的既有内部逻辑（原来散落在 chat.go 两处
// SSE/非流式分支里的重复代码）抽成一个共享 helper，chat.go 改调它，
// 行为逐字不变（ID 生成/ConfirmWord/TTL/存储单例全部沿用原实现）。
package ai

import (
	"fmt"

	"nas-panel/modules/tasks"
)

// buildPendingAction 是"生产一张待确认操作卡"的唯一实现：生成 ID、填
// ConfirmWord/CreatedAt、存入 defaultPendingStore。chat.go 的非流式/SSE
// 两个分支与 CreatePendingAction 共用这一个函数，不允许各自拼一份。
// summary 由调用方给定——chat.go 传 pendingActionSummary(toolName,argsJSON)
// 的计算结果；CreatePendingAction 传调用方（triggers）传入的 summary
// 参数，这样两条路径的"摘要怎么来"各自保留调用方语义，但"怎么落地成
// PendingAction"完全共用。
func buildPendingAction(toolName, argsJSON, summary string) PendingAction {
	action := PendingAction{
		ToolName: toolName,
		ArgsJSON: argsJSON,
		Summary:  summary,
	}
	action.ID = newPendingActionID()
	action.ConfirmWord = pendingActionConfirmWord
	action.CreatedAt = nowFunc()
	defaultPendingStore.Put(action)
	return action
}

// CreatePendingAction 是 triggers 建议接受链消费的导出面：toolName 必须
// 是已在 toolRegistry 注册的写工具（Write=true），否则拒绝——读工具/
// 未知工具都不允许借这条路径绕过"写操作才需要确认门"的既有语义。
// ID 生成/ConfirmWord/TTL/存储全部走 buildPendingAction，与 chat.go 的
// 内部路径是同一份实现（硬约束 4：不许复制粘贴两份逻辑）。
func CreatePendingAction(toolName, argsJSON, summary string) (id, confirmWord string, err error) {
	tool, ok := toolRegistry.Find(toolName)
	if !ok {
		return "", "", fmt.Errorf("未知工具 %s", toolName)
	}
	if !tool.Write {
		return "", "", fmt.Errorf("工具 %s 不是写工具，无需也不允许走确认门", toolName)
	}
	action := buildPendingAction(toolName, argsJSON, summary)
	return action.ID, action.ConfirmWord, nil
}

// StartTaskSummary 是 start_task 确认卡摘要构造函数的导出面，直接复用
// chat.go 内部的 startTaskSummary——triggers.AcceptSuggestion 构造
// "接受建议→固化 start_task pending action"时，摘要文案与模型主动请求
// start_task 时展示的摘要完全同源（硬约束 5：禁止复制文案）。
func StartTaskSummary(job tasks.Job) string {
	return startTaskSummary(job)
}
