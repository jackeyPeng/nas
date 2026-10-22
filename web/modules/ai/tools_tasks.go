package ai

import (
	"context"
	"fmt"
	"time"

	"nas-panel/common"
	"nas-panel/modules/tasks"
)

// taskPlanBudget 是 plan_task 同步扫描的预算上限（F1 简化：异步扫描排在
// 后续优化，聊天内等待可接受——有 thinking 态）。
const taskPlanBudget = 120 * time.Second

// nasTasksTools 返回 nas 域全部 7 个任务工具（plan/start/cancel/pause/
// resume/list/detail）。Write 标记与计划工具表逐字对应：
// plan_task/pause_task/get_task_list/get_task_detail 只读；
// start_task/cancel_task/resume_task 走确认门（真正"动文件"的操作都在
// 门上，plan_task 只读扫描无副作用——确认门语义不弱于 spec 原始设计，
// 偏离已在计划 Self-Review 记录）。
func nasTasksTools() []Tool {
	return []Tool{
		{
			Name:        "plan_task",
			Description: "扫描并生成一个任务计划（目前只支持清理重复照片/视频），返回影响面预估（组数/文件数/释放空间），不会执行任何文件操作。",
			Write:       false,
			Params: []ParamSpec{
				{Name: "kind", Type: "string", Description: "任务类型", Required: true, Enum: []string{"photo_dedupe"}},
				{Name: "scope", Type: "string", Description: "池内相对路径范围，留空表示扫描全池", Required: false},
			},
			Exec: execPlanTask,
		},
		{
			Name:        "start_task",
			Description: "启动一个已生成计划（pending_confirm 状态）的任务，开始真正移动文件。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "job_id", Type: "string", Description: "任务 ID", Required: true},
			},
			Exec: execStartTask,
		},
		{
			Name:        "cancel_task",
			Description: "取消一个任务（等待确认中/运行中/已暂停均可取消）。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "job_id", Type: "string", Description: "任务 ID", Required: true},
			},
			Exec: execCancelTask,
		},
		{
			Name:        "pause_task",
			Description: "暂停一个正在运行的任务（暂停本身不具破坏性，无需确认）。",
			Write:       false,
			Params: []ParamSpec{
				{Name: "job_id", Type: "string", Description: "任务 ID", Required: true},
			},
			Exec: execPauseTask,
		},
		{
			Name:        "resume_task",
			Description: "恢复一个已暂停的任务，继续移动文件。",
			Write:       true,
			Params: []ParamSpec{
				{Name: "job_id", Type: "string", Description: "任务 ID", Required: true},
			},
			Exec: execResumeTask,
		},
		{
			Name:        "get_task_list",
			Description: "获取最近的任务列表（按创建时间新到旧）。",
			Write:       false,
			Params: []ParamSpec{
				{Name: "limit", Type: "integer", Description: "最多返回多少条（默认 10）", Required: false},
			},
			Exec: execGetTaskList,
		},
		{
			Name:        "get_task_detail",
			Description: "获取单个任务的详情（状态/进度/结果/错误）。",
			Write:       false,
			Params: []ParamSpec{
				{Name: "job_id", Type: "string", Description: "任务 ID", Required: true},
			},
			Exec: execGetTaskDetail,
		},
	}
}

// execPlanTask 是 plan_task 的执行入口：同步扫描（120s 预算）直调
// tasks.PlanPhotoDedupe，返回 {job_id, impact, partial} JSON。kind 目前
// 只有 "photo_dedupe"（Enum 唯一值，ValidateArgs 已挡掉其它取值），scope
// 池外/穿越路径时 PlanPhotoDedupe 返回 error，这里转成 {"error":...}
// JSON 字符串（工具层不 panic，不 return err——对照 tools_nas_read.go 的
// 既有约定：Exec 的 error 返回值只在真正的基础设施失败时使用）。
//
// 只读但建 job 行：补一条 common.LogAudit（无 *http.Request 可用，走
// common.LogAuditRequest 的底层变体，actor 固定 "AI"——对照
// tools_nas_read.go 里只读工具本身不审计、但 plan_task 会在 jobs 表落一
// 条新行，属于"有副作用的只读"，比照 diagnostics.RunItem 这类异步启动
// 的写工具审计粒度，在工具层而不是只在 chat.go 的统一 AI_TOOL 审计里
// 记一次更利于日后单独检索"谁通过AI生成过哪些任务计划"）。
func execPlanTask(ctx context.Context, args map[string]interface{}) (string, error) {
	kind, _ := args["kind"].(string)
	scope, _ := args["scope"].(string)

	if kind != "photo_dedupe" {
		return marshalToolResult(map[string]string{"error": "未支持的任务类型: " + kind}), nil
	}

	job, partial, err := tasks.PlanPhotoDedupe(scope, taskPlanBudget)
	if err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}

	common.LogAudit("AI", "AI生成任务计划", "TASKS", "/api/ai/chat",
		fmt.Sprintf("kind=%s scope=%s job_id=%s groups=%d files=%d", kind, scope, job.ID, job.Impact.Groups, job.Impact.Files),
		"success", "")

	resp := map[string]interface{}{
		"job_id": job.ID,
		"impact": map[string]interface{}{
			"groups": job.Impact.Groups,
			"files":  job.Impact.Files,
			"bytes":  job.Impact.Bytes,
		},
		"partial": partial,
	}
	if partial {
		resp["note"] = "扫描未完整（预算内未扫完全部文件），已生成部分计划"
	}
	return marshalToolResult(resp), nil
}

// execStartTask 是 start_task 的确认执行入口（确认门之后由
// actions.go executeConfirmedAction 调用）：Exec=tasks.StartJob(job_id)。
// job 必须处于 pending_confirm，否则 StartJob 的状态机校验返回错误——
// 这里原样转成 {"error":...} JSON 字符串透传给模型（模型给不出、也不
// 允许给步骤明细，plan 在 PlanPhotoDedupe 服务端固化，start_task 只接受
// job_id 一个参数）。
func execStartTask(ctx context.Context, args map[string]interface{}) (string, error) {
	jobID, _ := args["job_id"].(string)
	if err := tasks.StartJob(jobID); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]interface{}{"message": "任务已启动", "job_id": jobID}), nil
}

// execCancelTask 是 cancel_task 的确认执行入口：Exec=tasks.CancelJob。
func execCancelTask(ctx context.Context, args map[string]interface{}) (string, error) {
	jobID, _ := args["job_id"].(string)
	if err := tasks.CancelJob(jobID); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]interface{}{"message": "任务已取消", "job_id": jobID}), nil
}

// execPauseTask 是 pause_task 的直接执行入口（只读工具，agent loop 直接
// 调用，无需确认门——暂停不具破坏性）。
func execPauseTask(ctx context.Context, args map[string]interface{}) (string, error) {
	jobID, _ := args["job_id"].(string)
	if err := tasks.PauseJob(jobID); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]interface{}{"message": "任务已暂停", "job_id": jobID}), nil
}

// execResumeTask 是 resume_task 的确认执行入口：Exec=tasks.ResumeJob
// （恢复=继续动文件，确认门语义同 start_task）。
func execResumeTask(ctx context.Context, args map[string]interface{}) (string, error) {
	jobID, _ := args["job_id"].(string)
	if err := tasks.ResumeJob(jobID); err != nil {
		return marshalToolResult(map[string]string{"error": err.Error()}), nil
	}
	return marshalToolResult(map[string]interface{}{"message": "任务已恢复", "job_id": jobID}), nil
}

// execGetTaskList 是 get_task_list 的执行入口：直调 tasks.ListJobs。
func execGetTaskList(ctx context.Context, args map[string]interface{}) (string, error) {
	limit := clampInt(argInt(args, "limit"), 10, 1, 200)
	return marshalToolResult(map[string]interface{}{"jobs": tasks.ListJobs(limit)}), nil
}

// execGetTaskDetail 是 get_task_detail 的执行入口：直调 tasks.GetJob。
// job_id 参数经由 database/sql 参数化查询传递（tasks 包内 GetJob 用 `?`
// 占位符，不做字符串拼接），注入字符串（如 `'; DROP TABLE jobs;--`）
// 只会被当成普通字符串参数值去比较 id 列，查无结果，不影响库结构。
func execGetTaskDetail(ctx context.Context, args map[string]interface{}) (string, error) {
	jobID, _ := args["job_id"].(string)
	job, ok := tasks.GetJob(jobID)
	if !ok {
		return marshalToolResult(map[string]string{"error": "任务不存在或已清理"}), nil
	}
	return marshalToolResult(job), nil
}
