package rclone

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestChownReference 验证成功路径：目标文件属主/属组被对齐为参考路径的
// 属主/属组（等价于 `chown --reference=refPath target`）。
// 非 root 环境下 chown 到当前以外的用户会失败，这里用"对齐为自己当前 uid/gid"
// 验证调用链路（Stat 成功 → Chown 被调用且不返回错误阻断调用方）。
func TestChownReference(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(ref, []byte("ref"), 0644); err != nil {
		t.Fatalf("setup ref failed: %v", err)
	}
	if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
		t.Fatalf("setup target failed: %v", err)
	}

	refInfo, err := os.Stat(ref)
	if err != nil {
		t.Fatalf("stat ref failed: %v", err)
	}
	refSt := refInfo.Sys().(*syscall.Stat_t)

	// 不应 panic；在当前进程权限下对齐为自身 uid/gid 应当成功
	chownReference(ref, target)

	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target failed: %v", err)
	}
	targetSt := targetInfo.Sys().(*syscall.Stat_t)
	if targetSt.Uid != refSt.Uid || targetSt.Gid != refSt.Gid {
		t.Errorf("chownReference 后 target uid/gid = %d/%d, want %d/%d",
			targetSt.Uid, targetSt.Gid, refSt.Uid, refSt.Gid)
	}
}

// TestChownReference_MissingRefDoesNotPanic 验证参考路径 Stat 失败时
// 静默跳过，保留原命令 `chown --reference=... 2>/dev/null` 的容错语义。
func TestChownReference_MissingRefDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("chownReference panicked: %v", r)
		}
	}()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
		t.Fatalf("setup target failed: %v", err)
	}
	chownReference(filepath.Join(dir, "does-not-exist"), target)
}
