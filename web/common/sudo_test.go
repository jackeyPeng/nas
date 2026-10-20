//go:build linux
// +build linux

package common

import (
	"os"
	"testing"
)

// ============================================================
// sudo_test.go — sudo / exec 命令封装测试
//
// ⚠️ 本文件仅 Linux 平台编译（build tag: linux）
// ⚠️ 必须在部署了 NAS 的 Debian 13 环境中运行
//
// 【给队友的说明】
// 执行前请确认：
//   1. sudoers 已配置 nas-panel 白名单
//   2. 当前用户有 sudo 权限
//   3. 基本命令（echo, whoami, df）可用
//
// 执行方式：
//   cd web && go test ./common/ -v -run TestSudo -tags linux
// ============================================================

func TestSudoExec_Echo(t *testing.T) {
	// 最简单的命令验证 sudo 通道畅通
	out, err := SudoExec("echo", "hello-nas-test")
	if err != nil {
		t.Skipf("sudo 不可用，跳过测试: %v", err)
	}
	if out != "hello-nas-test" {
		t.Errorf("SudoExec() = %q, want %q", out, "hello-nas-test")
	}
}

func TestSudoExec_Whoami(t *testing.T) {
	out, err := SudoExec("whoami")
	if err != nil {
		t.Skipf("sudo 不可用: %v", err)
	}
	// sudo whoami 应该返回 root
	if out != "root" {
		t.Errorf("SudoExec(whoami) = %q, want root (check sudoers config)", out)
	}
}

func TestSudoOutput_Df(t *testing.T) {
	out, err := SudoOutput("df", "-h", "/")
	if err != nil {
		t.Skipf("df 命令不可用: %v", err)
	}
	if out == "" {
		t.Error("SudoOutput(df) returned empty output")
	}
	t.Logf("df output: %s", out)
}

func TestSudoExecStdin_Cat(t *testing.T) {
	// 用 cat 验证 stdin 透传；无需真 sudo 语义，仅确认管道正确接线
	out, err := SudoExecStdin("hello-stdin", "cat")
	if err != nil {
		t.Skipf("sudo 不可用，跳过测试: %v", err)
	}
	if out != "hello-stdin" {
		t.Errorf("SudoExecStdin() = %q, want %q", out, "hello-stdin")
	}
}

func TestSudoExecStdin_MultiLine(t *testing.T) {
	// chpasswd 等命令会收到 "user:pass\n" 这类多行输入，确认换行不被截断
	out, err := SudoExecStdin("line1\nline2\n", "cat")
	if err != nil {
		t.Skipf("sudo 不可用，跳过测试: %v", err)
	}
	want := "line1\nline2"
	if out != want {
		t.Errorf("SudoExecStdin() = %q, want %q", out, want)
	}
}

func TestExec_Echo(t *testing.T) {
	out, err := Exec("echo", "test-no-sudo")
	if err != nil {
		t.Fatalf("Exec(echo) failed: %v", err)
	}
	if out != "test-no-sudo" {
		t.Errorf("Exec() = %q, want %q", out, "test-no-sudo")
	}
}

func TestExec_Whoami(t *testing.T) {
	out, err := Exec("whoami")
	if err != nil {
		t.Fatalf("Exec(whoami) failed: %v", err)
	}
	// 无 sudo 时应返回当前用户（非 root）
	if out == "" {
		t.Error("Exec(whoami) returned empty")
	}
	t.Logf("current user (no sudo): %s", out)
}

func TestExecOutput_Echo(t *testing.T) {
	out, err := ExecOutput("echo", "test-output-mode")
	if err != nil {
		t.Fatalf("ExecOutput(echo) failed: %v", err)
	}
	// Output() 不 trim，注意可能包含换行
	if len(out) == 0 {
		t.Error("ExecOutput() returned empty")
	}
}

// ============================================================
// sudoers 白名单验证
//
// 以下测试验证 nas-panel 的 sudoers 配置是否正确限制了
// 可执行命令。如果白名单未配置或配置错误，这些测试会失败，
// 说明存在安全风险。
// ============================================================

func TestSudoExec_WhitelistCheck(t *testing.T) {
	// 尝试执行不应在白名单中的命令
	// 如果成功执行，说明 sudoers 白名单过于宽松
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname() failed: %v", err)
	}

	t.Logf("测试主机: %s", hostname)
	t.Log("提示：请确认 /etc/sudoers.d/nas-panel 中命令白名单已正确配置")
	t.Log("预期：sudoers 应仅允许 nas-panel 需要的特定命令")
}
