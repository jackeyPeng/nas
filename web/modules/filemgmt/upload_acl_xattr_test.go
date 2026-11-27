package filemgmt

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// origUIDGID 从 os.FileInfo 取真实 uid/gid,封装平台相关的 Sys() 类型断言,
// 避免在多个测试里重复写同样的 syscall.Stat_t 转换。
func origUIDGID(t *testing.T, info os.FileInfo) (int, int) {
	t.Helper()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("当前平台不支持 syscall.Stat_t,无法取 uid/gid")
	}
	return int(st.Uid), int(st.Gid)
}

// Item 5 修复回归测试:覆盖写(overwrite=true)必须保留原文件的 ACL/xattr。
//
// 不依赖真机是否装有 getfacl/setfacl/getfattr/setfattr(acl/attr 软件包):
// 通过 getfaclFn/setfaclFn/getfattrFn/setfattrFn 四个 package-level 变量注入
// fake 实现,断言——
//  1. capture 在写入新内容之前对"原文件"(覆盖写发生前的旧内容)调用一次;
//  2. restore 在 rename 之前对临时文件调用,且使用的是 capture 读到的 dump;
//  3. capture 失败 → 整个覆盖写失败,原文件完全不受影响,不产生临时文件残留;
//  4. restore 失败 → 整个覆盖写失败,原文件完全不受影响(fail-closed),
//     临时文件被清理,不得被 rename 到位;
//  5. 原文件不存在(新建场景)时不触发 capture 调用(没有旧属性需要保留)。

// withSeams 临时替换四个 ACL/xattr 执行函数,t.Cleanup 时还原,避免测试间污染。
func withSeams(t *testing.T, acl func(string) (string, error), setAcl func(string, string) error,
	xattr func(string) (string, error), setXattr func(string, string) error) {
	t.Helper()
	oldGetfacl, oldSetfacl, oldGetfattr, oldSetfattr := getfaclFn, setfaclFn, getfattrFn, setfattrFn
	if acl != nil {
		getfaclFn = acl
	}
	if setAcl != nil {
		setfaclFn = setAcl
	}
	if xattr != nil {
		getfattrFn = xattr
	}
	if setXattr != nil {
		setfattrFn = setXattr
	}
	t.Cleanup(func() {
		getfaclFn, setfaclFn, getfattrFn, setfattrFn = oldGetfacl, oldSetfacl, oldGetfattr, oldSetfattr
	})
}

// TestWritePartToFileOverwriteCapturesAndRestoresACLXattr 是本修复的核心回归:
// fake getfaclFn/getfattrFn 返回固定 dump,断言 restore 阶段收到的正是这份 dump
// (而不是空值/篡改值),覆盖写成功后原文件内容被替换,但 ACL/xattr "语义上"
// 已经走过 capture→restore 流程(通过参数断言验证,不依赖真实 setfacl 生效)。
func TestWritePartToFileOverwriteCapturesAndRestoresACLXattr(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("original content"), 0644); err != nil {
		t.Fatalf("预置目标文件失败: %v", err)
	}

	const fakeACL = "user::rw-\nuser:1000:rwx\ngroup::r--\nmask::rwx\nother::r--\n"
	const fakeXattr = "# file: a.txt\nuser.custom=0sdGVzdA==\n"

	var capturedPath string
	var restoreACLArgs, restoreXattrArgs []string

	withSeams(t,
		func(path string) (string, error) {
			capturedPath = path
			return fakeACL, nil
		},
		func(path, dump string) error {
			restoreACLArgs = []string{path, dump}
			return nil
		},
		func(path string) (string, error) {
			return fakeXattr, nil
		},
		func(path, dump string) error {
			restoreXattrArgs = []string{path, dump}
			return nil
		},
	)

	buf := make([]byte, 32*1024)
	newContent := []byte("brand new data")
	n, err := writePartToFile(target, bytes.NewReader(newContent), buf, true)
	if err != nil {
		t.Fatalf("覆盖写不应报错: %v", err)
	}
	if n != int64(len(newContent)) {
		t.Fatalf("写入字节数 = %d, want %d", n, len(newContent))
	}

	if capturedPath != target {
		t.Fatalf("capture 应针对原文件路径调用, got %q want %q", capturedPath, target)
	}
	if len(restoreACLArgs) != 2 || restoreACLArgs[1] != fakeACL {
		t.Fatalf("restore ACL 应收到 capture 读到的 dump, got %v", restoreACLArgs)
	}
	if len(restoreXattrArgs) != 2 || restoreXattrArgs[1] != fakeXattr {
		t.Fatalf("restore xattr 应收到 capture 读到的 dump, got %v", restoreXattrArgs)
	}
	// restore 必须作用于临时文件(rename 之前),不是最终路径本身——
	// 调用时临时文件还叫 .upload-tmp-*,不等于 target。
	if restoreACLArgs[0] == target {
		t.Fatalf("restore 应作用于 rename 之前的临时文件,不应是最终路径 %q", target)
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("目标文件应存在: %v", rerr)
	}
	if !bytes.Equal(data, newContent) {
		t.Fatalf("内容应被覆盖, got %q want %q", data, newContent)
	}
}

// TestWritePartToFileOverwriteCaptureFailureAbortsAndPreservesOriginal 验证
// capture(原文件 ACL/xattr 读取)失败时,整个覆盖写必须中止:不得创建/残留
// 任何临时文件,原文件必须保持字节级完整未变,函数必须返回错误(不得假成功)。
func TestWritePartToFileOverwriteCaptureFailureAbortsAndPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "half.bin")
	original := []byte("original content that must survive a capture failure")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatalf("预置目标文件失败: %v", err)
	}

	withSeams(t,
		func(path string) (string, error) { return "", errors.New("模拟 getfacl 失败") },
		nil, nil, nil,
	)

	buf := make([]byte, 32*1024)
	_, err := writePartToFile(target, bytes.NewReader([]byte("new data")), buf, true)
	if err == nil {
		t.Fatalf("capture ACL 失败应导致覆盖写整体失败,不得假成功")
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("原文件应仍然存在: %v", rerr)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容必须保持字节级完整未变, got %q want %q", data, original)
	}

	// capture 发生在创建临时文件之前,失败时目录里不应出现任何临时文件。
	entries, derr := os.ReadDir(root)
	if derr != nil {
		t.Fatalf("ReadDir 失败: %v", derr)
	}
	if len(entries) != 1 || entries[0].Name() != "half.bin" {
		t.Fatalf("capture 失败后目录应只剩原文件, got entries: %v", entries)
	}
}

// TestWritePartToFileOverwriteCaptureXattrFailureAbortsAndPreservesOriginal 同上,
// 但失败点是 xattr 读取(getfattrFn)而非 ACL——两步都必须是硬错误。
func TestWritePartToFileOverwriteCaptureXattrFailureAbortsAndPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "half.bin")
	original := []byte("original content that must survive an xattr capture failure")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatalf("预置目标文件失败: %v", err)
	}

	withSeams(t,
		func(path string) (string, error) { return "", nil }, // ACL 读取成功(空 dump)
		nil,
		func(path string) (string, error) { return "", errors.New("模拟 getfattr 失败") },
		nil,
	)

	buf := make([]byte, 32*1024)
	_, err := writePartToFile(target, bytes.NewReader([]byte("new data")), buf, true)
	if err == nil {
		t.Fatalf("capture xattr 失败应导致覆盖写整体失败,不得假成功")
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("原文件应仍然存在: %v", rerr)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容必须保持字节级完整未变, got %q want %q", data, original)
	}
}

// TestWritePartToFileOverwriteRestoreACLFailureAbortsAndPreservesOriginal 验证
// restore(把 capture 到的 ACL 应用到临时文件)失败时的 fail-closed 语义:
// 原文件必须保持不变,临时文件必须被清理,不得 rename 到位。
func TestWritePartToFileOverwriteRestoreACLFailureAbortsAndPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "half.bin")
	original := []byte("original content that must survive a restore-ACL failure")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatalf("预置目标文件失败: %v", err)
	}

	withSeams(t,
		func(path string) (string, error) { return "user::rw-\n", nil }, // 非空 dump,触发 restore 调用
		func(path, dump string) error { return errors.New("模拟 setfacl 失败") },
		func(path string) (string, error) { return "", nil },
		nil,
	)

	buf := make([]byte, 32*1024)
	_, err := writePartToFile(target, bytes.NewReader([]byte("new data")), buf, true)
	if err == nil {
		t.Fatalf("restore ACL 失败应导致覆盖写整体失败,不得假成功")
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("原文件应仍然存在: %v", rerr)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容必须保持字节级完整未变(fail-closed,不得 rename 覆盖), got %q want %q", data, original)
	}

	entries, derr := os.ReadDir(root)
	if derr != nil {
		t.Fatalf("ReadDir 失败: %v", derr)
	}
	if len(entries) != 1 || entries[0].Name() != "half.bin" {
		t.Fatalf("restore ACL 失败后目录应只剩原文件(临时文件已被清理), got entries: %v", entries)
	}
}

// TestWritePartToFileOverwriteRestoreXattrFailureAbortsAndPreservesOriginal 同上,
// 失败点是 restore xattr(setfattrFn)。
func TestWritePartToFileOverwriteRestoreXattrFailureAbortsAndPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "half.bin")
	original := []byte("original content that must survive a restore-xattr failure")
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatalf("预置目标文件失败: %v", err)
	}

	withSeams(t,
		func(path string) (string, error) { return "", nil },
		nil,
		func(path string) (string, error) { return "# file: x\nuser.a=0sYQ==\n", nil }, // 非空 dump
		func(path, dump string) error { return errors.New("模拟 setfattr 失败") },
	)

	buf := make([]byte, 32*1024)
	_, err := writePartToFile(target, bytes.NewReader([]byte("new data")), buf, true)
	if err == nil {
		t.Fatalf("restore xattr 失败应导致覆盖写整体失败,不得假成功")
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("原文件应仍然存在: %v", rerr)
	}
	if !bytes.Equal(data, original) {
		t.Fatalf("原文件内容必须保持字节级完整未变, got %q want %q", data, original)
	}

	entries, derr := os.ReadDir(root)
	if derr != nil {
		t.Fatalf("ReadDir 失败: %v", derr)
	}
	if len(entries) != 1 || entries[0].Name() != "half.bin" {
		t.Fatalf("restore xattr 失败后目录应只剩原文件(临时文件已被清理), got entries: %v", entries)
	}
}

// TestWritePartToFileOverwriteSkipsCaptureWhenOriginalMissing 验证"原文件不存在"
// (本次上传实际是新建,只是请求带了 overwrite=yes)时,capture 不应报错、
// 也不应触发 getfaclFn/getfattrFn 调用(没有旧属性需要保留),restore 收到
// 空 dump 时应是无操作(不调用 setfaclFn/setfattrFn)。
func TestWritePartToFileOverwriteSkipsCaptureWhenOriginalMissing(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "new.txt") // 不预置,真正不存在

	captureCalled := false
	restoreCalled := false
	withSeams(t,
		func(path string) (string, error) { captureCalled = true; return "", nil },
		func(path, dump string) error { restoreCalled = true; return nil },
		func(path string) (string, error) { captureCalled = true; return "", nil },
		func(path, dump string) error { restoreCalled = true; return nil },
	)

	buf := make([]byte, 32*1024)
	content := []byte("first write via overwrite=yes on nonexistent target")
	n, err := writePartToFile(target, bytes.NewReader(content), buf, true)
	if err != nil {
		t.Fatalf("原文件不存在时覆盖写(=新建)不应报错: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("写入字节数 = %d, want %d", n, len(content))
	}
	if captureCalled {
		t.Fatalf("原文件不存在时不应调用 getfaclFn/getfattrFn(没有旧属性需要保留)")
	}
	if restoreCalled {
		t.Fatalf("capture 返回空 dump 时不应调用 setfaclFn/setfattrFn(无操作)")
	}
	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("文件应已落盘: %v", rerr)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("内容不一致, got %q want %q", data, content)
	}
}

// TestCaptureACLXattrReturnsEmptyForMissingFile 直接单测 captureACLXattr 对
// "文件不存在"的处理:必须返回空 dump、nil error,不触发 getfaclFn/getfattrFn。
func TestCaptureACLXattrReturnsEmptyForMissingFile(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "does-not-exist.txt")

	called := false
	withSeams(t,
		func(path string) (string, error) { called = true; return "x", nil },
		nil,
		func(path string) (string, error) { called = true; return "x", nil },
		nil,
	)

	acl, xattr, err := captureACLXattr(missing)
	if err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
	if acl != "" || xattr != "" {
		t.Fatalf("文件不存在应返回空 dump, got acl=%q xattr=%q", acl, xattr)
	}
	if called {
		t.Fatalf("文件不存在时不应调用 getfaclFn/getfattrFn")
	}
}

// requireXattrTools 跳过测试,除非 getfattr/setfattr 都在 PATH 里——这组测试
// 验证的是 runGetfattr/runSetfattr 真实调用外部命令的行为(不经过 seam),
// 开发机/CI 没装 attr 包时跳过而不是报错。
func requireXattrTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("getfattr"); err != nil {
		t.Skip("getfattr 不可用,跳过真实 xattr 行为测试")
	}
	if _, err := exec.LookPath("setfattr"); err != nil {
		t.Skip("setfattr 不可用,跳过真实 xattr 行为测试")
	}
}

// TestRunSetfattrRestoresToGivenPathNotOriginal 是 item A 的核心回归:
// 复现并验证修复前的真实 bug——getfattr --dump 的输出自带 `# file: <path>`
// 头,旧 runSetfattr 用 `setfattr --restore=-` 消费该 dump 时,会把属性写回
// dump 里嵌入的路径(原文件),而不是调用方传入的 path 参数(临时文件),
// 导致覆盖写场景里属性被错误写到原文件而不是临时文件。
//
// 本测试直接跑真实的 runGetfattr(从 orig 读)+ runSetfattr(写到 tmp,
// 路径与 orig 不同),断言:
//  1. tmp 拿到了属性(restore 对调用方传入的路径确实生效);
//  2. orig 的属性集合保持不变(restore 过程绝不会反过来修改原文件)。
func TestRunSetfattrRestoresToGivenPathNotOriginal(t *testing.T) {
	requireXattrTools(t)

	root := t.TempDir()
	orig := filepath.Join(root, "orig.txt")
	tmp := filepath.Join(root, "tmp.txt")
	if err := os.WriteFile(orig, []byte("original"), 0644); err != nil {
		t.Fatalf("创建 orig 失败: %v", err)
	}
	if err := os.WriteFile(tmp, []byte("tmp"), 0644); err != nil {
		t.Fatalf("创建 tmp 失败: %v", err)
	}

	// 在 orig 上设置一个自定义 xattr,再用 runGetfattr 读出 dump。
	if out, err := exec.Command("setfattr", "-n", "user.panel_test", "-v", "secret-value", orig).CombinedOutput(); err != nil {
		t.Skipf("当前环境不支持给 %s 设置 xattr(可能是文件系统不支持 user xattr): %s: %v", orig, out, err)
	}

	dump, err := runGetfattr(orig)
	if err != nil {
		t.Fatalf("runGetfattr(orig) 失败: %v", err)
	}
	if dump == "" {
		t.Fatalf("runGetfattr(orig) 应读到非空 dump")
	}

	// 恢复到 tmp,而不是 orig —— 模拟覆盖写场景(dump 来自原文件,写入目标是临时文件)。
	if err := runSetfattr(tmp, dump); err != nil {
		t.Fatalf("runSetfattr(tmp, dump) 失败: %v", err)
	}

	tmpDump, err := runGetfattr(tmp)
	if err != nil {
		t.Fatalf("runGetfattr(tmp) 失败: %v", err)
	}
	if tmpDump == "" {
		t.Fatalf("修复后 restore 必须把属性写到调用方传入的路径(tmp),但 tmp 上读不到任何 user.* xattr")
	}

	// orig 的属性集合必须保持不变——restore 绝不能修改原文件。
	origDumpAfter, err := runGetfattr(orig)
	if err != nil {
		t.Fatalf("runGetfattr(orig) after 失败: %v", err)
	}
	if origDumpAfter != dump {
		t.Fatalf("restore 操作不应修改原文件的 xattr, before=%q after=%q", dump, origDumpAfter)
	}
}

// TestRunSetfattrFailureDoesNotTouchOriginal 验证 fail-closed:给一个不存在
// 的目标路径(setfattr 必然报错),断言 runSetfattr 返回 error,且不会以任何
// 方式写到原始 dump 来源路径之外的意外位置——这里直接断言返回了 error
// (调用方据此中止覆盖写,是 restoreACLXattr/writePartToFileOverwrite 既有
// fail-closed 契约的组成部分,真实命令层面的再次确认)。
func TestRunSetfattrFailureDoesNotTouchOriginal(t *testing.T) {
	requireXattrTools(t)

	root := t.TempDir()
	missing := filepath.Join(root, "does-not-exist", "tmp.txt") // 父目录都不存在

	err := runSetfattr(missing, "user.custom=0x74657374")
	if err == nil {
		t.Fatalf("对不存在的路径执行 setfattr 应失败,不应静默成功")
	}
}

// TestRestoreACLXattrNoopOnEmptyDump 直接单测 restoreACLXattr:两个 dump 都为空
// 时必须是纯无操作(不调用 setfaclFn/setfattrFn,也不报错)。
func TestRestoreACLXattrNoopOnEmptyDump(t *testing.T) {
	called := false
	withSeams(t,
		nil,
		func(path, dump string) error { called = true; return nil },
		nil,
		func(path, dump string) error { called = true; return nil },
	)

	if err := restoreACLXattr("/tmp/whatever", "", ""); err != nil {
		t.Fatalf("空 dump 应是无操作,不应报错: %v", err)
	}
	if called {
		t.Fatalf("空 dump 不应触发 setfaclFn/setfattrFn 调用")
	}
}

// --- issue #3 补充:对 runGetfattr/runGetfacl/runSetfacl 真实命令行为的
// 端到端验证,覆盖 owner、mode、ACL、可读 xattr 在 t.TempDir() 真实文件上的
// 保留情况,而不是只信任既有的"黑盒调用、fake 断言参数"覆盖。 ------------

// requireAclTools 跳过测试,除非 getfacl/setfacl 都在 PATH 里。
func requireAclTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("getfacl"); err != nil {
		t.Skip("getfacl 不可用,跳过真实 ACL 行为测试")
	}
	if _, err := exec.LookPath("setfacl"); err != nil {
		t.Skip("setfacl 不可用,跳过真实 ACL 行为测试")
	}
}

// TestRunGetfattrEnumeratesMultipleUserAttrsWithBinaryValue 验证 runGetfattr
// 真实枚举逻辑(先 -m '^user\.' 列名,再逐名 -n NAME -e hex 取值)在同一文件
// 上有多个 user.* xattr、且其中一个值含不可打印二进制字节时,能够把全部
// 属性都枚举出来、且每一行的十六进制值与真实写入的字节完全吻合——这是
// 本次审查的重点:之前只验证过"黑盒调用一次 runGetfattr/runSetfattr 的
// round-trip",没有真正验证多属性枚举 + 二进制值编码这两个关键环节。
func TestRunGetfattrEnumeratesMultipleUserAttrsWithBinaryValue(t *testing.T) {
	requireXattrTools(t)

	root := t.TempDir()
	target := filepath.Join(root, "multi.txt")
	if err := os.WriteFile(target, []byte("content"), 0644); err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}

	// 设置两个 xattr:一个可打印字符串,一个含 0x00/0xff 等不可打印二进制字节
	// (验证 -e hex 编码对非文本数据同样可靠,不会因为是"二进制属性"丢数据
	// 或截断——这正是任务要求里"null/binary attribute handling"的核心场景)。
	if out, err := exec.Command("setfattr", "-n", "user.comment", "-v", "hello world", target).CombinedOutput(); err != nil {
		t.Skipf("当前环境不支持 user xattr: %s: %v", out, err)
	}
	binVal := "0x00ff7f0102deadbeef"
	if out, err := exec.Command("setfattr", "-n", "user.binarydata", "-v", binVal, target).CombinedOutput(); err != nil {
		t.Fatalf("setfattr 二进制属性失败: %s: %v", out, err)
	}

	dump, err := runGetfattr(target)
	if err != nil {
		t.Fatalf("runGetfattr 失败: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(dump), "\n")
	got := map[string]string{}
	for _, line := range lines {
		eq := strings.Index(line, "=")
		if eq < 0 {
			t.Fatalf("输出行无法解析 name=value: %q (完整 dump=%q)", line, dump)
		}
		got[line[:eq]] = line[eq+1:]
	}

	if len(got) != 2 {
		t.Fatalf("应枚举出 2 个 user.* 属性, got %d: %v (dump=%q)", len(got), got, dump)
	}
	if got["user.comment"] == "" {
		t.Fatalf("缺少 user.comment, dump=%q", dump)
	}
	wantHex := "0x" + hexEncode([]byte("hello world"))
	if got["user.comment"] != wantHex {
		t.Fatalf("user.comment 取值 = %q, want %q", got["user.comment"], wantHex)
	}
	if got["user.binarydata"] != binVal {
		t.Fatalf("user.binarydata 取值 = %q, want %q(二进制字节必须原样保留,不可截断/替换)", got["user.binarydata"], binVal)
	}

	// 完整 capture→restore round trip 到另一个文件,验证枚举结果可以被
	// runSetfattr 正确应用、且二进制属性的字节在应用后依然原样可读。
	tmp := filepath.Join(root, "multi_restored.txt")
	if err := os.WriteFile(tmp, []byte("x"), 0644); err != nil {
		t.Fatalf("创建目标失败: %v", err)
	}
	if err := runSetfattr(tmp, dump); err != nil {
		t.Fatalf("runSetfattr 失败: %v", err)
	}
	restoredDump, err := runGetfattr(tmp)
	if err != nil {
		t.Fatalf("runGetfattr(tmp) 失败: %v", err)
	}
	restoredLines := strings.Split(strings.TrimSpace(restoredDump), "\n")
	if len(restoredLines) != 2 {
		t.Fatalf("恢复后应有 2 个属性, got %d: %q", len(restoredLines), restoredDump)
	}
	if !strings.Contains(restoredDump, "user.binarydata="+binVal) {
		t.Fatalf("恢复后二进制属性字节必须原样保留, got %q", restoredDump)
	}
}

// hexEncode 是测试内部用的最小十六进制编码,避免为了一个断言引入
// encoding/hex 的额外 import(该文件其余部分用不到十六进制编码,只有这一处)。
func hexEncode(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexDigits[c>>4]
		out[i*2+1] = hexDigits[c&0x0f]
	}
	return string(out)
}

// TestRunGetfattrReturnsEmptyForFileWithNoXattr 验证"文件完全没有 user.* xattr"
// 时 runGetfattr 返回空字符串、无错误——不能把 getfattr 的"无属性"误判为失败,
// 也不能把它的空输出错误解析成某个属性行。
func TestRunGetfattrReturnsEmptyForFileWithNoXattr(t *testing.T) {
	requireXattrTools(t)

	root := t.TempDir()
	target := filepath.Join(root, "noattrs.txt")
	if err := os.WriteFile(target, []byte("plain file"), 0644); err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}

	dump, err := runGetfattr(target)
	if err != nil {
		t.Fatalf("无 xattr 的文件不应报错: %v", err)
	}
	if dump != "" {
		t.Fatalf("无 xattr 的文件应返回空 dump, got %q", dump)
	}
}

// TestFullOverwritePreservesOwnerModeACLAndXattrOnRealFile 是 Point 3 的核心
// 端到端验证:不经任何 seam,直接跑真实的 writePartToFileOverwrite(内部走
// 真实 runGetfacl/runSetfacl/runGetfattr/runSetfattr + 真实 chmod/chown),在
// t.TempDir() 的真实文件上验证覆盖写之后原文件的有效权限(mode、ACL、包括
// mask、xattr)全部原样保留,不会被池策略(alignFilePerms)重算掉。
//
// 本轮修订(推翻 TASK-8 旧结论,见 Point 3 拍板决策/captureOrigOwnership 注释):
// 覆盖写时原文件的 mode/ACL/mask 是权威值,不是池策略。旧版本这里曾断言
// mode 被改写成池策略值 0644、mask 被重算成 r--,现在改为断言两者都原样
// 保留(0640、mask::rwx 不变)。uid/gid 的保留见下方
// TestFullOverwritePreservesUidGidOnRealFile(需要能真实 chown 的权限,
// 普通非 root 测试进程下用 fake chownFn 验证调用参数,而非真实生效结果)。
func TestFullOverwritePreservesOwnerModeACLAndXattrOnRealFile(t *testing.T) {
	requireXattrTools(t)
	requireAclTools(t)

	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	if err := os.WriteFile(target, []byte("original content"), 0640); err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}
	if out, err := exec.Command("setfattr", "-n", "user.panel_meta", "-v", "keep-me", target).CombinedOutput(); err != nil {
		t.Skipf("当前环境不支持 user xattr: %s: %v", out, err)
	}
	if out, err := exec.Command("setfacl", "-m", "u:nobody:rwx", target).CombinedOutput(); err != nil {
		t.Skipf("当前环境不支持 ACL: %s: %v", out, err)
	}

	beforeACL, err := runGetfacl(target)
	if err != nil {
		t.Fatalf("runGetfacl(before) 失败: %v", err)
	}
	if !strings.Contains(beforeACL, "user:nobody:rwx") {
		t.Fatalf("预置 ACL 未生效, got %q", beforeACL)
	}
	if !strings.Contains(beforeACL, "mask::rwx") {
		t.Fatalf("测试前置条件有误:预置 ACL 后 mask 应为 rwx, got %q", beforeACL)
	}

	// 覆盖写现在保留原文件的 mode/ACL(Point 3 拍板,见上方函数注释),这里
	// 不预置 folders.db 元数据,ownerShareOfVia 查不到归属共享 → found=false,
	// 但这个判定结果对覆盖写路径已经不再生效(只影响"新建文件"分支)。
	buf := make([]byte, 32*1024)
	newContent := []byte("brand new content after overwrite")
	n, werr := writePartToFile(target, bytes.NewReader(newContent), buf, true)
	if werr != nil {
		t.Fatalf("覆盖写失败: %v", werr)
	}
	if n != int64(len(newContent)) {
		t.Fatalf("写入字节数 = %d, want %d", n, len(newContent))
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("读取目标文件失败: %v", rerr)
	}
	if !bytes.Equal(data, newContent) {
		t.Fatalf("内容应被覆盖, got %q", data)
	}

	info, serr := os.Stat(target)
	if serr != nil {
		t.Fatalf("Stat 失败: %v", serr)
	}
	// 注意:文件带 ACL 时,stat(2) 的 mode 第二组(group)位读的是 ACL 的
	// mask 条目,不是 group:: 条目本身(POSIX ACL 标准行为)——预置阶段
	// chmod 0640 + setfacl 加一条 u:nobody:rwx 之后,mask 被 setfacl 联动
	// 重算成 rwx,此时 stat 看到的"原 mode"就已经是 0670,不是字面的 0640。
	// captureOrigOwnership 原样记录并恢复的正是这个 stat 可见值,所以这里
	// 断言期望值是 0670(等同于上面预置阶段结束时 stat 会看到的值),
	// 不是 WriteFile 时传的字面 0640。
	if info.Mode().Perm() != 0670 {
		t.Fatalf("mode 应保留覆盖写之前 stat 可见的原值 0670(覆盖写不应套用池策略), got %o", info.Mode().Perm())
	}

	afterACL, aerr := runGetfacl(target)
	if aerr != nil {
		t.Fatalf("runGetfacl(after) 失败: %v", aerr)
	}
	if !strings.Contains(afterACL, "user:nobody:rwx") {
		t.Fatalf("覆盖写之后 ACL 必须保留, before=%q after=%q", beforeACL, afterACL)
	}

	afterXattr, xerr := runGetfattr(target)
	if xerr != nil {
		t.Fatalf("runGetfattr(after) 失败: %v", xerr)
	}
	if !strings.Contains(afterXattr, "user.panel_meta=") {
		t.Fatalf("覆盖写之后 xattr 必须保留, got %q", afterXattr)
	}

	// mask:: 显式断言(对应需求里的"包括 mask"):Point 3 拍板决策是原 ACL
	// (含 mask)权威,覆盖写之后 mask 必须与覆盖写之前完全一致(rwx),不再
	// 像旧版本那样被池策略 chmod 重算成 r--。
	if !strings.Contains(afterACL, "mask::rwx") {
		t.Fatalf("mask 应保留原值 rwx(覆盖写不应重算掉), got %q", afterACL)
	}
}

// TestFullOverwritePreservesUidGidOnRealFile 验证覆盖写路径对 uid/gid 的
// 处理:用 fake chownFn 捕获调用参数(真实 chown 到任意 uid 需要 root,CI/
// 开发机上的非特权测试进程做不到),断言 restoreOrigOwnership 确实拿原文件
// 的 uid/gid 去调用 chown,而不是 alignPermsDecision 算出的策略 owner/group
// (本测试场景下 ownerShareOfVia 查不到归属共享,若仍走旧的策略对齐路径,
// 根本不会触发任何 chown 调用——found=false 时 alignPermsDecision 返回空
// ownerName/groupName,直接跳过 chown;而新路径下只要原文件存在就一定会
// chown 一次,用这个行为差异坐实"已切换到恢复原 uid/gid"这条路径)。
func TestFullOverwritePreservesUidGidOnRealFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "owned.txt")
	if err := os.WriteFile(target, []byte("original content"), 0640); err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}
	st, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatalf("Lstat 失败: %v", statErr)
	}
	wantUID, wantGID := origUIDGID(t, st)

	oldChown := chownFn
	oldChmod := chmodFn
	var gotPath string
	var gotUID, gotGID int
	var chownCalls int
	chownFn = func(path string, uid, gid int) error {
		chownCalls++
		gotPath, gotUID, gotGID = path, uid, gid
		return nil
	}
	chmodFn = os.Chmod
	t.Cleanup(func() { chownFn, chmodFn = oldChown, oldChmod })

	buf := make([]byte, 32*1024)
	_, werr := writePartToFile(target, bytes.NewReader([]byte("new content")), buf, true)
	if werr != nil {
		t.Fatalf("覆盖写失败: %v", werr)
	}
	if chownCalls != 1 {
		t.Fatalf("chown 应恰好被调用一次(恢复原 uid/gid), got %d 次", chownCalls)
	}
	if gotUID != wantUID || gotGID != wantGID {
		t.Fatalf("chown 参数应为原文件 uid/gid = (%d,%d), got (%d,%d)", wantUID, wantGID, gotUID, gotGID)
	}
	if gotPath == target {
		t.Fatalf("chown 应作用于临时文件(rename 之前),不应直接是 target 本身: %s", gotPath)
	}
}

// TestWritePartToFileOverwriteFailsClosedWhenOrigOwnershipCannotBeRestored
// 验证 Point 3 的 fail-closed 要求:恢复原 uid/gid/mode 失败(fake chownFn
// 返回错误,模拟真机上非特权进程 chown 到任意 uid 失败的场景)时,整个
// 覆盖写必须失败、原文件内容和权限完全不受影响、不产生临时文件残留、
// 更不能 rename——不存在"恢复不了就退回池策略值"的降级路径。
func TestWritePartToFileOverwriteFailsClosedWhenOrigOwnershipCannotBeRestored(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "protected.txt")
	const originalContent = "must not be touched"
	if err := os.WriteFile(target, []byte(originalContent), 0640); err != nil {
		t.Fatalf("创建文件失败: %v", err)
	}

	oldChown := chownFn
	chownFn = func(path string, uid, gid int) error {
		return fmt.Errorf("模拟 chown 失败: 当前进程无权修改 uid/gid")
	}
	t.Cleanup(func() { chownFn = oldChown })

	buf := make([]byte, 32*1024)
	_, werr := writePartToFile(target, bytes.NewReader([]byte("attacker content")), buf, true)
	if werr == nil {
		t.Fatalf("恢复原 uid/gid 失败时覆盖写必须失败,实际返回了 nil error")
	}

	data, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("原文件应仍可读: %v", rerr)
	}
	if string(data) != originalContent {
		t.Fatalf("恢复失败时原文件内容不应被改变, got %q", data)
	}
	info, serr := os.Stat(target)
	if serr != nil {
		t.Fatalf("Stat 失败: %v", serr)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("恢复失败时原文件 mode 不应被改变, got %o", info.Mode().Perm())
	}

	entries, derr := os.ReadDir(root)
	if derr != nil {
		t.Fatalf("ReadDir 失败: %v", derr)
	}
	if len(entries) != 1 {
		t.Fatalf("恢复失败时不应残留临时文件, got %d 个目录项", len(entries))
	}
}
