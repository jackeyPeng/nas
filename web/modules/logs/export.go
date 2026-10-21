package logs

import "nas-panel/common"

// maxAuditQueryLimit 是 QueryAudit 可接受的最大 limit（硬上限，防止 AI
// 工具一次性拖出整个 audit.db）。
const maxAuditQueryLimit = 200

// QueryAudit 导出审计日志查询内核，供 AI 工具域（get_audit_logs）等进程内
// 调用方直接复用 common.QueryAuditLog 的查询内核（= handleQueryLogs 用的
// 同一条路径），不走 HTTP 自环，也不复制 SQL 逻辑。
//
// limit 会被 clamp 到 [1, maxAuditQueryLimit]；sinceHours 转换成
// common.QueryAuditLog 认的 days 参数（向上取整为至少 1 天，0 表示不限
// 时间窗口——QueryAuditLog 的 days<=0 语义本就是"不过滤时间"）。
// sinceHours 自身的 168 上限由调用方（AI 工具 Exec）在参数层 clamp，
// 这里只做 limit 的硬上限兜底，双重防御。
func QueryAudit(limit int, sinceHours int) ([]common.AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxAuditQueryLimit {
		limit = maxAuditQueryLimit
	}
	days := 0
	if sinceHours > 0 {
		days = (sinceHours + 23) / 24 // 向上取整，保证窗口至少覆盖 sinceHours
	}
	entries, _, err := common.QueryAuditLog("", "", "", days, limit, 0)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []common.AuditEntry{}
	}
	return entries, nil
}
