package health

// Summary 导出健康检查汇总内核（F2 Task 4），供 triggers 模块的周报生成
// 只读调用：跑一遍全部检查项，按状态计数返回 pass/warn/fail 三档
// （ok/info 归 pass，warn 归 warn，error 归 fail）。薄导出层，逻辑机械
// 抽取自 handleHealth 的检查项聚合分支，不改既有行为（不触发
// emitHealthTransitions，周报只读不产生事件噪音）。
func Summary() (pass, warn, fail int) {
	checks := []Check{
		checkSystem(),
		checkStorage(),
		checkNetwork(),
		checkBackup(),
		checkSecurity(),
		checkCloudSync(),
	}
	for _, c := range checks {
		switch c.Status {
		case "warn":
			warn++
		case "error":
			fail++
		default: // ok, info
			pass++
		}
	}
	return pass, warn, fail
}
