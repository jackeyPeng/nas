package diskmgmt

import (
	"errors"
	"strings"
	"testing"
)

// ═══════════════════════════════════════════════════
// stripArrayLines —— 表驱动纯函数测试
// ═══════════════════════════════════════════════════

func TestStripArrayLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "多ARRAY行+默认行保留",
			in: "HOMEHOST <system>\n" +
				"MAILADDR root\n" +
				"ARRAY /dev/md0 metadata=1.2 name=nas:0 UUID=1234abcd:5678efgh:90ab1234:cdef5678\n" +
				"ARRAY /dev/md1 metadata=1.2 name=nas:1 UUID=aaaa0000:bbbb1111:cccc2222:dddd3333\n",
			want: "HOMEHOST <system>\nMAILADDR root\n",
		},
		{
			name: "无ARRAY行原样返回",
			in:   "HOMEHOST <system>\nMAILADDR root\n",
			want: "HOMEHOST <system>\nMAILADDR root\n",
		},
		{
			name: "空文件",
			in:   "",
			want: "",
		},
		{
			name: "只有空白字符",
			in:   "   \n  \n",
			want: "   \n  \n",
		},
		{
			name: "CRLF容错",
			in: "HOMEHOST <system>\r\n" +
				"ARRAY /dev/md0 metadata=1.2 UUID=1234abcd:5678efgh:90ab1234:cdef5678\r\n" +
				"MAILADDR root\r\n",
			want: "HOMEHOST <system>\nMAILADDR root\n",
		},
		{
			name: "只有ARRAY行删完为空",
			in:   "ARRAY /dev/md0 metadata=1.2 UUID=1234abcd:5678efgh:90ab1234:cdef5678\n",
			want: "",
		},
		{
			name: "注释行与空行保留",
			in: "# this is a comment\n" +
				"\n" +
				"ARRAY /dev/md0 metadata=1.2 UUID=1234abcd:5678efgh:90ab1234:cdef5678\n" +
				"HOMEHOST <system>\n",
			want: "# this is a comment\n\nHOMEHOST <system>\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripArrayLines(c.in)
			if got != c.want {
				t.Errorf("stripArrayLines(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// ═══════════════════════════════════════════════════
// upsertArrayLine —— ARRAY 行替换/追加（同 UUID 异设备名场景）
// ═══════════════════════════════════════════════════

func TestUpsertArrayLine(t *testing.T) {
	const uuid1 = "1234abcd:5678efgh:90ab1234:cdef5678"
	const uuid2 = "aaaa0000:bbbb1111:cccc2222:dddd3333"

	cases := []struct {
		name     string
		existing string
		newLine  string
		want     string
	}{
		{
			name:     "空文件追加",
			existing: "",
			newLine:  "ARRAY /dev/md0 metadata=1.2 UUID=" + uuid1,
			want:     "ARRAY /dev/md0 metadata=1.2 UUID=" + uuid1 + "\n",
		},
		{
			name:     "无同UUID行则追加到末尾",
			existing: "HOMEHOST <system>\nARRAY /dev/md1 metadata=1.2 UUID=" + uuid2 + "\n",
			newLine:  "ARRAY /dev/md0 metadata=1.2 UUID=" + uuid1,
			want:     "HOMEHOST <system>\nARRAY /dev/md1 metadata=1.2 UUID=" + uuid2 + "\nARRAY /dev/md0 metadata=1.2 UUID=" + uuid1 + "\n",
		},
		{
			// 核心场景：重启后设备名从 md0 漂移到 md127，UUID 不变——
			// 必须原地替换旧行（保留原有位置），而不是在文件里叠两条。
			name:     "同UUID异设备名原地替换",
			existing: "HOMEHOST <system>\nARRAY /dev/md0 metadata=1.2 name=nas:0 UUID=" + uuid1 + "\nMAILADDR root\n",
			newLine:  "ARRAY /dev/md127 metadata=1.2 name=nas:0 UUID=" + uuid1,
			want:     "HOMEHOST <system>\nARRAY /dev/md127 metadata=1.2 name=nas:0 UUID=" + uuid1 + "\nMAILADDR root\n",
		},
		{
			name:     "重复调用不叠加行",
			existing: "ARRAY /dev/md0 metadata=1.2 UUID=" + uuid1 + "\n",
			newLine:  "ARRAY /dev/md0 metadata=1.2 UUID=" + uuid1,
			want:     "ARRAY /dev/md0 metadata=1.2 UUID=" + uuid1 + "\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := upsertArrayLine(c.existing, c.newLine)
			if got != c.want {
				t.Errorf("upsertArrayLine() = %q, want %q", got, c.want)
			}
		})
	}
}

// ═══════════════════════════════════════════════════
// extractArrayUUID / findArrayLineByUUID 边界
// ═══════════════════════════════════════════════════

func TestExtractArrayUUID(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantOK   bool
		wantUUID string
	}{
		{"正常ARRAY行", "ARRAY /dev/md0 metadata=1.2 name=nas:0 UUID=1234abcd:5678efgh:90ab1234:cdef5678", true, "1234abcd:5678efgh:90ab1234:cdef5678"},
		{"大写UUID归一化小写", "ARRAY /dev/md0 metadata=1.2 UUID=ABCD1234:EFGH5678", true, "abcd1234:efgh5678"},
		{"非ARRAY行", "HOMEHOST <system>", false, ""},
		{"ARRAY行无UUID字段", "ARRAY /dev/md0 metadata=1.2 name=nas:0", false, ""},
		{"空行", "", false, ""},
		{"注释行", "# ARRAY /dev/md0 UUID=1234", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uuid, ok := extractArrayUUID(c.line)
			if ok != c.wantOK {
				t.Fatalf("extractArrayUUID(%q) ok = %v, want %v", c.line, ok, c.wantOK)
			}
			if ok && uuid != c.wantUUID {
				t.Errorf("extractArrayUUID(%q) uuid = %q, want %q", c.line, uuid, c.wantUUID)
			}
		})
	}
}

// ═══════════════════════════════════════════════════
// persistRAIDConfig —— 注入 runner + raidConfWriter stub，五路径
// ═══════════════════════════════════════════════════

// stubRunner 按命令名（第一个参数）分发到对应的响应函数，模拟
// mdadm/cat/update-initramfs 三类命令各自独立的输出/失败。
type stubRunner struct {
	scan    func(args ...string) (string, error)
	cat     func(args ...string) (string, error)
	initram func(args ...string) (string, error)
}

func (s *stubRunner) run(name string, args ...string) (string, error) {
	switch name {
	case "/usr/sbin/mdadm":
		if s.scan != nil {
			return s.scan(args...)
		}
	case "cat":
		if s.cat != nil {
			return s.cat(args...)
		}
	case "update-initramfs":
		if s.initram != nil {
			return s.initram(args...)
		}
	}
	return "", nil
}

// withRAIDConfWriterStub 替换 raidConfWriter 为内存 stub，测试结束后还原。
// writes 记录每次调用的 (path, content)；returnErr 非 nil 时模拟落盘失败。
func withRAIDConfWriterStub(t *testing.T, returnErr error) *[]string {
	t.Helper()
	var writes []string
	orig := raidConfWriter
	raidConfWriter = func(path, content string) error {
		writes = append(writes, content)
		return returnErr
	}
	t.Cleanup(func() { raidConfWriter = orig })
	return &writes
}

const testUUID = "1234abcd:5678efgh:90ab1234:cdef5678"
const testScanLine = "ARRAY /dev/md0 metadata=1.2 name=nas:0 UUID=" + testUUID

func TestPersistRAIDConfig_Success(t *testing.T) {
	writes := withRAIDConfWriterStub(t, nil)

	// 写后验证靠再次 cat——用一个可变标志模拟"落盘后 cat 返回最新内容"。
	var written string
	origWriter := raidConfWriter
	raidConfWriter = func(path, content string) error {
		written = content
		return nil
	}
	t.Cleanup(func() { raidConfWriter = origWriter })

	r := &stubRunner{
		scan: func(args ...string) (string, error) { return testScanLine, nil },
		cat: func(args ...string) (string, error) {
			return written, nil
		},
		initram: func(args ...string) (string, error) { return "", nil },
	}

	warning, err := persistRAIDConfig(r.run, "/dev/md0")
	if err != nil {
		t.Fatalf("persistRAIDConfig() error = %v, want nil", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
	if written == "" {
		t.Fatalf("mdadm.conf 未落盘")
	}
	if !strings.Contains(written, testUUID) {
		t.Errorf("落盘内容缺少目标 UUID: %q", written)
	}
	_ = writes
}

func TestPersistRAIDConfig_ScanFailed(t *testing.T) {
	withRAIDConfWriterStub(t, nil)

	r := &stubRunner{
		scan: func(args ...string) (string, error) { return "", errors.New("mdadm: no such device") },
	}

	warning, err := persistRAIDConfig(r.run, "/dev/md0")
	if err == nil {
		t.Fatal("persistRAIDConfig() error = nil, want non-nil（scan 失败必须报错，不得假成功）")
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty（err 非 nil 时 warning 不应有内容）", warning)
	}
}

func TestPersistRAIDConfig_ScanEmpty(t *testing.T) {
	withRAIDConfWriterStub(t, nil)

	r := &stubRunner{
		// scan 命令本身没报错，但输出里没有 ARRAY/UUID 行（如设备刚创建、
		// mdadm 版本输出格式异常等）——必须当失败处理，不能假成功。
		scan: func(args ...string) (string, error) { return "", nil },
	}

	warning, err := persistRAIDConfig(r.run, "/dev/md0")
	if err == nil {
		t.Fatal("persistRAIDConfig() error = nil, want non-nil（scan 输出为空必须报错）")
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
}

func TestPersistRAIDConfig_VerifyFailed(t *testing.T) {
	// 落盘成功（writer 不报错），但写后验证的 cat 读回的内容里没有目标
	// UUID（模拟落盘后被其他进程覆盖/磁盘异常截断）——必须报错。
	withRAIDConfWriterStub(t, nil)

	callCount := 0
	r := &stubRunner{
		scan: func(args ...string) (string, error) { return testScanLine, nil },
		cat: func(args ...string) (string, error) {
			callCount++
			// 第一次 cat（读现有 conf）返回空；第二次 cat（写后验证）
			// 故意返回不含目标 UUID 的内容，模拟验证失败。
			if callCount == 1 {
				return "", nil
			}
			return "HOMEHOST <system>\n", nil
		},
	}

	warning, err := persistRAIDConfig(r.run, "/dev/md0")
	if err == nil {
		t.Fatal("persistRAIDConfig() error = nil, want non-nil（写后验证失败必须报错）")
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
}

func TestPersistRAIDConfig_InitramfsWarning(t *testing.T) {
	var written string
	raidConfWriter = func(path, content string) error {
		written = content
		return nil
	}
	orig := raidConfWriter
	t.Cleanup(func() { raidConfWriter = orig })

	r := &stubRunner{
		scan: func(args ...string) (string, error) { return testScanLine, nil },
		cat:  func(args ...string) (string, error) { return written, nil },
		initram: func(args ...string) (string, error) {
			return "update-initramfs: command not found", errors.New("exit status 127")
		},
	}

	warning, err := persistRAIDConfig(r.run, "/dev/md0")
	if err != nil {
		t.Fatalf("persistRAIDConfig() error = %v, want nil（initramfs 失败不应 fatal）", err)
	}
	if warning == "" {
		t.Error("warning 为空，want 非空（initramfs 失败必须带出 warning，不得静默）")
	}
	if !strings.Contains(warning, "update-initramfs") {
		t.Errorf("warning = %q, 应提及 update-initramfs", warning)
	}
}

func TestPersistRAIDConfig_WriteFailed(t *testing.T) {
	withRAIDConfWriterStub(t, errors.New("disk full"))

	r := &stubRunner{
		scan: func(args ...string) (string, error) { return testScanLine, nil },
		cat:  func(args ...string) (string, error) { return "", nil },
	}

	warning, err := persistRAIDConfig(r.run, "/dev/md0")
	if err == nil {
		t.Fatal("persistRAIDConfig() error = nil, want non-nil（落盘失败必须报错）")
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
}

// ═══════════════════════════════════════════════════
// resetRAIDConfig —— 注入 runner + raidConfWriter stub
// ═══════════════════════════════════════════════════

func TestResetRAIDConfig_RemovesArrayLinesOnly(t *testing.T) {
	var written string
	wrote := false
	raidConfWriter = func(path, content string) error {
		written = content
		wrote = true
		return nil
	}
	orig := raidConfWriter
	t.Cleanup(func() { raidConfWriter = orig })

	r := &stubRunner{
		cat: func(args ...string) (string, error) {
			return "HOMEHOST <system>\nARRAY /dev/md0 metadata=1.2 UUID=" + testUUID + "\nMAILADDR root\n", nil
		},
		initram: func(args ...string) (string, error) { return "", nil },
	}

	warning, err := resetRAIDConfig(r.run)
	if err != nil {
		t.Fatalf("resetRAIDConfig() error = %v, want nil", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
	if !wrote {
		t.Fatal("内容有变化时应落盘，但未调用 writer")
	}
	if strings.Contains(written, "ARRAY") {
		t.Errorf("落盘内容仍含 ARRAY 行: %q", written)
	}
	if !strings.Contains(written, "HOMEHOST") || !strings.Contains(written, "MAILADDR") {
		t.Errorf("落盘内容应保留 HOMEHOST/MAILADDR: %q", written)
	}
}

func TestResetRAIDConfig_NoChangeSkipsWrite(t *testing.T) {
	wrote := false
	raidConfWriter = func(path, content string) error {
		wrote = true
		return nil
	}
	orig := raidConfWriter
	t.Cleanup(func() { raidConfWriter = orig })

	r := &stubRunner{
		cat: func(args ...string) (string, error) { return "HOMEHOST <system>\nMAILADDR root\n", nil },
	}

	warning, err := resetRAIDConfig(r.run)
	if err != nil {
		t.Fatalf("resetRAIDConfig() error = %v, want nil", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
	if wrote {
		t.Error("内容无变化时不应落盘")
	}
}

func TestResetRAIDConfig_CatFailedSkipsGracefully(t *testing.T) {
	wrote := false
	raidConfWriter = func(path, content string) error {
		wrote = true
		return nil
	}
	orig := raidConfWriter
	t.Cleanup(func() { raidConfWriter = orig })

	r := &stubRunner{
		cat: func(args ...string) (string, error) { return "", errors.New("no such file") },
	}

	warning, err := resetRAIDConfig(r.run)
	if err != nil {
		t.Fatalf("resetRAIDConfig() error = %v, want nil（文件不存在不是失败）", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
	if wrote {
		t.Error("cat 失败（文件不存在）时不应尝试落盘")
	}
}

func TestResetRAIDConfig_InitramfsWarning(t *testing.T) {
	raidConfWriter = func(path, content string) error { return nil }
	orig := raidConfWriter
	t.Cleanup(func() { raidConfWriter = orig })

	r := &stubRunner{
		cat: func(args ...string) (string, error) {
			return "ARRAY /dev/md0 metadata=1.2 UUID=" + testUUID + "\n", nil
		},
		initram: func(args ...string) (string, error) {
			return "", errors.New("exit status 1")
		},
	}

	warning, err := resetRAIDConfig(r.run)
	if err != nil {
		t.Fatalf("resetRAIDConfig() error = %v, want nil（initramfs 失败不应 fatal）", err)
	}
	if warning == "" {
		t.Error("warning 为空，want 非空（initramfs 失败必须带出 warning）")
	}
}

func TestResetRAIDConfig_WriteFailed(t *testing.T) {
	raidConfWriter = func(path, content string) error { return errors.New("disk full") }
	orig := raidConfWriter
	t.Cleanup(func() { raidConfWriter = orig })

	r := &stubRunner{
		cat: func(args ...string) (string, error) {
			return "ARRAY /dev/md0 metadata=1.2 UUID=" + testUUID + "\n", nil
		},
	}

	warning, err := resetRAIDConfig(r.run)
	if err == nil {
		t.Fatal("resetRAIDConfig() error = nil, want non-nil（落盘失败必须报错）")
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty", warning)
	}
}

// nil runner 应回退到生产实现 sudoRunner（仅验证不 panic/编译期类型兼容，
// 不实际调用——会触发真实 sudo，违反约束，这里只测 resetRAIDConfig/
// persistRAIDConfig 接受 nil 不崩溃的分支由上面各用例间接覆盖：均显式传
// 非 nil 的 stub runner，nil 分支留给生产路径，不在单测里触发真实命令。
