package main

// 架构边界测试（spec v4 §三/§三b，F1 Task 1 Step 1）
//
// 目的：用机器检查固化分层约束，不靠 review 记忆。
//   1) 每个 modules/<X> 的非测试 .go 文件 import 的 modules/<Y>（Y≠X）必须
//      在 allowedModuleDeps[X] 白名单内——基线按现状真实 import 扫描生成
//      （见 OPENCODE-REPORT-F1-R1.md 的基线 map 记录），新增跨模块依赖必须
//      改这里的白名单 = 过 review。
//   2) tasks 不得 import ai 的任何文件（引擎无模型依赖，可独立测试）。
//   3) 核心模块（diskmgmt/users/firewall/monitor/diagnostics/logs/system/
//      filemgmt/vault/rclone/events/hardware/update/storage/version/dashboard）
//      不得 import ai/tasks/triggers（防止反向依赖把核心模块绑死在 agent 层）。
//
// 实现用 go/parser 直接解析源文件 AST，不起 go list 子进程（更快、不依赖
// 环境里的 go 工具链 PATH 设置，纯静态分析）。

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// allowedModuleDeps 是按现状真实 import 扫描生成的基线白名单：
// module -> 允许 import 的 nas-panel/modules/* 集合（common 恒允许；stdlib 恒允许）。
// 扫描方法与结果详见 OPENCODE-REPORT-F1-R1.md「边界测试基线 map」一节。
var allowedModuleDeps = map[string][]string{
	"ai":       {"diagnostics", "diskmgmt", "firewall", "logs", "monitor", "system", "users", "tasks"},
	"filemgmt": {"diskmgmt"},
	"health":   {"dashboard"},
	"monitor":  {"dashboard"},
	"services": {"dashboard"},
	"update":   {"version"},
	"users":    {"diskmgmt"},
	"tasks":    {"filemgmt"},
	"triggers": {"tasks", "ai", "diskmgmt", "notifications", "health"}, // F2 Task 2 开白名单；health 是 Task 4 周报新增依赖（GenerateWeeklyReport 读 health.Summary）
}

// coreModules 是不得反向依赖 ai/tasks/triggers 的核心模块集合。
var coreModules = []string{
	"diskmgmt", "users", "firewall", "monitor", "diagnostics", "logs", "system",
	"filemgmt", "vault", "rclone", "events", "hardware", "update", "storage",
	"version", "dashboard",
}

// restrictedConsumers 约束"谁能 import 某个叶子模块"：module -> 允许
// import 它的模块集合（白名单外一律违规）。notifications 是零上游依赖的
// 收件箱叶子模块；F2 Task 4 落地周报时把 triggers 加入这里——triggers
// 直接 import notifications.AddNotification 是拍板的例外（计划 Task 4
// 「依赖拍板」：经 eventbus 绕行会把整个报告 JSON 负载塞进事件参数，
// 得不偿失）。notifications 仍不得反向 import 任何业务模块。
var restrictedConsumers = map[string][]string{
	"notifications": {"triggers"},
}

// moduleImportPrefix 是跨模块 import 路径前缀；Go module 名见 web/go.mod。
const moduleImportPrefix = "nas-panel/modules/"

// scanModuleImports 遍历 modulesDir 下每个子目录的非测试 .go 文件，
// 返回 module -> 依赖的其它 module 集合（去重）。
func scanModuleImports(t *testing.T, modulesDir string) map[string]map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		t.Fatalf("read modules dir: %v", err)
	}

	deps := map[string]map[string]bool{}
	fset := token.NewFileSet()

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		modName := e.Name()
		dir := filepath.Join(modulesDir, modName)
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read module dir %s: %v", dir, err)
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(f.Name(), "_test.go") {
				continue // 规则只约束非测试文件（辅助测试代码允许更宽松的 import，如 fixture 生成）
			}
			path := filepath.Join(dir, f.Name())
			node, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range node.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				if !strings.HasPrefix(importPath, moduleImportPrefix) {
					continue
				}
				dep := strings.TrimPrefix(importPath, moduleImportPrefix)
				// 只取顶层模块名（防止将来出现 modules/x/sub 这种子包路径）
				if idx := strings.Index(dep, "/"); idx >= 0 {
					dep = dep[:idx]
				}
				if dep == modName {
					continue // 自引用不算跨模块依赖
				}
				if deps[modName] == nil {
					deps[modName] = map[string]bool{}
				}
				deps[modName][dep] = true
			}
		}
	}
	return deps
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestArchBoundaries(t *testing.T) {
	modulesDir, err := filepath.Abs("modules")
	if err != nil {
		t.Fatalf("resolve modules dir: %v", err)
	}
	if _, err := os.Stat(modulesDir); err != nil {
		t.Fatalf("modules dir not found at %s: %v", modulesDir, err)
	}

	deps := scanModuleImports(t, modulesDir)

	// 规则 1：每个模块的跨模块依赖必须落在白名单内。
	for mod, depSet := range deps {
		allowed := allowedModuleDeps[mod]
		var got []string
		for d := range depSet {
			got = append(got, d)
		}
		sort.Strings(got)
		for _, d := range got {
			if !contains(allowed, d) {
				t.Errorf("规则1违规: modules/%s 非测试文件 import 了 modules/%s，"+
					"不在白名单 allowedModuleDeps[%q]=%v 内。"+
					"若这是有意的新依赖，需更新 allowedModuleDeps（过 review）",
					mod, d, mod, allowed)
			}
		}
	}

	// 规则 2：tasks 不得 import ai（任何文件，含测试文件——引擎必须无模型依赖）。
	tasksDir := filepath.Join(modulesDir, "tasks")
	if _, err := os.Stat(tasksDir); err == nil {
		fset := token.NewFileSet()
		files, _ := os.ReadDir(tasksDir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") {
				continue
			}
			path := filepath.Join(tasksDir, f.Name())
			node, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range node.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				if importPath == moduleImportPrefix+"ai" || strings.HasPrefix(importPath, moduleImportPrefix+"ai/") {
					t.Errorf("规则2违规: %s import 了 %s —— tasks 引擎不得依赖 ai 模块", path, importPath)
				}
			}
		}
	}

	// 规则 3：核心模块不得 import ai/tasks/triggers。
	forbidden := map[string]bool{"ai": true, "tasks": true, "triggers": true}
	for _, mod := range coreModules {
		for d := range deps[mod] {
			if forbidden[d] {
				t.Errorf("规则3违规: modules/%s（核心模块）import 了 modules/%s —— 核心模块不得反向依赖 ai/tasks/triggers",
					mod, d)
			}
		}
	}

	// 规则 4：restrictedConsumers 里的叶子模块只能被白名单内的模块 import。
	for leaf, allowedConsumers := range restrictedConsumers {
		for mod, depSet := range deps {
			if mod == leaf {
				continue
			}
			if !depSet[leaf] {
				continue
			}
			if !contains(allowedConsumers, mod) {
				t.Errorf("规则4违规: modules/%s import 了 modules/%s，"+
					"不在 restrictedConsumers[%q]=%v 内——%s 是零上游依赖的叶子模块",
					mod, leaf, leaf, allowedConsumers, leaf)
			}
		}
	}
}
