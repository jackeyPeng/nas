package users

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// issue #4 回归测试：setPrivateDirQuota 的移除配额分支（quotaGB<=0）与
// diskmgmt/quota.go 的 setFolderQuota 共享同一个根因 bug——此前顺序是
// "先删 /etc/projects、/etc/projid 映射，再 project -C"，但 project -C 要靠
// 映射把 project 名字解析成 ID，先删映射会让清除可能失败或落空；同时两步的
// 返回值都被忽略，无论成功失败都对外报告"已取消"。
//
// 全部使用 t.TempDir() 下的假文件 + sudoExecFn/execOutputFn/safeWriteFileFn
// seam，不碰真实 /etc/projects、/etc/projid、xfs_quota。

// stubUsersQuotaDirAndMount 让 setPrivateDirQuota 跳过真实 sudo test -d /
// df 探测，恒为"目录存在、挂载点为 /mnt/fake"，专注测试配额移除分支的顺序
// 与错误传播（与路径探测本身无关，路径探测不在 issue #4 范围内）。
func stubUsersQuotaDirAndMount(t *testing.T) {
	t.Helper()
	origDirExists, origFindMount := dirExistsFn, findMountPointFn
	dirExistsFn = func(string) bool { return true }
	findMountPointFn = func(string) string { return "/mnt/fake" }
	t.Cleanup(func() {
		dirExistsFn = origDirExists
		findMountPointFn = origFindMount
	})
}

func setupUsersQuotaTestFiles(t *testing.T) (projectsPath, projidPath string) {
	t.Helper()
	dir := t.TempDir()
	projectsPath = filepath.Join(dir, "projects")
	projidPath = filepath.Join(dir, "projid")

	origProjects, origProjid := quotaProjectsFile, quotaProjidFile
	quotaProjectsFile = projectsPath
	quotaProjidFile = projidPath
	t.Cleanup(func() {
		quotaProjectsFile = origProjects
		quotaProjidFile = origProjid
	})
	return projectsPath, projidPath
}

func writeUsersQuotaMapping(t *testing.T, projectsPath, projidPath, projName string, id int, path string) {
	t.Helper()
	if err := os.WriteFile(projectsPath, []byte(fmt.Sprintf("%d:%s\n", id, path)), 0644); err != nil {
		t.Fatalf("写入 projects 失败: %v", err)
	}
	if err := os.WriteFile(projidPath, []byte(fmt.Sprintf("%s:%d\n", projName, id)), 0644); err != nil {
		t.Fatalf("写入 projid 失败: %v", err)
	}
}

// swapUsersQuotaExecFns 用真实文件 I/O 替代 common.SudoExec/common.ExecOutput，
// 使 execOutputFn("cat", path) 读到测试写入的假文件内容，safeWriteFileFn 直接
// 写回假文件；sudoExecFn 则由各测试按需定制（成功、失败、或内联断言）。
func swapUsersQuotaExecFns(t *testing.T, sudoExec func(name string, args ...string) (string, error)) {
	t.Helper()
	origSudo, origExecOut, origWrite := sudoExecFn, execOutputFn, safeWriteFileFn
	sudoExecFn = sudoExec
	execOutputFn = func(name string, args ...string) (string, error) {
		if name == "cat" && len(args) == 1 {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return "", err
			}
			return string(data), nil
		}
		return origExecOut(name, args...)
	}
	safeWriteFileFn = func(path, content string) error {
		return os.WriteFile(path, []byte(content), 0644)
	}
	t.Cleanup(func() {
		sudoExecFn = origSudo
		execOutputFn = origExecOut
		safeWriteFileFn = origWrite
	})
}

func TestSetPrivateDirQuotaRemoveCallsProjectClearBeforeDeletingMapping(t *testing.T) {
	stubUsersQuotaDirAndMount(t)
	projectsPath, projidPath := setupUsersQuotaTestFiles(t)
	writeUsersQuotaMapping(t, projectsPath, projidPath, "home_alice", 1000, "/data/nas1/alice")

	var mappingResolvableDuringProjectClear bool
	swapUsersQuotaExecFns(t, func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "project -C home_alice") {
			// project -C 执行时，映射必须仍然存在（否则 xfs_quota 无法把
			// project 名字解析成 ID），这里直接检查映射文件当前内容来证明
			// 调用顺序是"先 project -C，后删映射"。
			if id := findProjectID("home_alice"); id == 1000 {
				mappingResolvableDuringProjectClear = true
			}
			return "", nil
		}
		return "", nil
	})

	if err := setPrivateDirQuota("alice", 0); err != nil {
		t.Fatalf("setPrivateDirQuota 返回了意外的错误: %v", err)
	}

	if !mappingResolvableDuringProjectClear {
		t.Fatal("project -C 执行时映射已经不可解析——说明映射被过早删除")
	}

	// project -C 成功后，映射必须已被删除。
	if id := findProjectID("home_alice"); id != -1 {
		t.Fatalf("project -C 成功后映射应已删除，仍解析到 id=%d", id)
	}
}

func TestSetPrivateDirQuotaRemoveProjectClearFailureDoesNotDeleteMapping(t *testing.T) {
	stubUsersQuotaDirAndMount(t)
	projectsPath, projidPath := setupUsersQuotaTestFiles(t)
	writeUsersQuotaMapping(t, projectsPath, projidPath, "home_bob", 1001, "/data/nas1/bob")

	swapUsersQuotaExecFns(t, func(name string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "project -C home_bob") {
			return "xfs_quota: project not found", fmt.Errorf("exit status 1")
		}
		return "", nil
	})

	err := setPrivateDirQuota("bob", 0)
	if err == nil {
		t.Fatal("project -C 失败时 setPrivateDirQuota 应返回错误，却返回 nil")
	}

	// 映射必须原样保留（清除失败=什么都没变，可以安全重试）。
	if id := findProjectID("home_bob"); id != 1001 {
		t.Fatalf("project -C 失败后映射应保持不变，findProjectID 返回 %d（预期 1001）", id)
	}
	data, err := os.ReadFile(projectsPath)
	if err != nil {
		t.Fatalf("读取 projects 失败: %v", err)
	}
	if !strings.Contains(string(data), "/data/nas1/bob") {
		t.Fatalf("projects 映射被意外改动: %q", string(data))
	}
}

func TestSetPrivateDirQuotaRemoveMappingDeleteFailureReportsPartialState(t *testing.T) {
	stubUsersQuotaDirAndMount(t)
	projectsPath, projidPath := setupUsersQuotaTestFiles(t)
	writeUsersQuotaMapping(t, projectsPath, projidPath, "home_carol", 1002, "/data/nas1/carol")

	origWrite := safeWriteFileFn
	swapUsersQuotaExecFns(t, func(name string, args ...string) (string, error) {
		return "", nil // project -C 成功
	})
	// project -C 成功之后，删映射阶段的 safeWriteFileFn 失败。
	safeWriteFileFn = func(path, content string) error {
		return fmt.Errorf("磁盘已满（模拟）")
	}
	t.Cleanup(func() { safeWriteFileFn = origWrite })

	err := setPrivateDirQuota("carol", 0)
	if err == nil {
		t.Fatal("删映射失败时 setPrivateDirQuota 应返回错误（如实报告部分失败），却返回 nil")
	}
	if !strings.Contains(err.Error(), "已清除") {
		t.Fatalf("错误信息应说明文件系统配额已清除、仅映射删除失败，实际: %v", err)
	}
}

// setPrivateDirQuota 的"设置配额"分支（quotaGB>0）依赖真实 dirExists、
// findMountPoint（df 命令）、common.MountQuotaSupport（findmnt），这三者
// 在 users 包里都没有 seam（超出 issue #4 的顺序 bug 范围，不在本轮改动中
// 新增——详见 TASK-REPORT-STORAGE.md 的已知限制），因此该分支无法在不新增
// 更大面积 seam 的前提下用 mock 验证，这里不测试、只测试本轮修复涉及的
// 移除配额分支（quotaGB<=0）。
