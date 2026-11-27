package diskmgmt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"nas-panel/common"
)

// Item 7 修复的回归测试：
//  1. setFolderQuota 并发调用（不同文件夹）不能算出相同的 projectID 并互相
//     覆盖 /etc/projects、/etc/projid 映射。
//  2. appendToFile/removeProjectEntry/updateProjectPath 的写入失败必须向上
//     传播，不能被调用方静默吞掉。
//
// 全部使用 t.TempDir() 下的假文件 + sudoExecFn/safeWriteFileFn seam，不碰
// 真实 /etc/projects、/etc/projid、xfs_quota。

func setupQuotaTestFiles(t *testing.T) (projectsPath, projidPath string) {
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

func stubQuotaSudoExecSuccess(t *testing.T) {
	t.Helper()
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		return "", nil
	}
	t.Cleanup(func() { sudoExecFn = orig })
}

// stubMountQuotaSupported 让 setFolderQuota 的配额能力探测恒为"支持"，绕开
// 真实 findmnt（测试用的 /mnt/pool1 之类假路径本就探测不到）。
func stubMountQuotaSupported(t *testing.T) {
	t.Helper()
	orig := mountQuotaSupportFn
	mountQuotaSupportFn = func(mountPoint string) common.QuotaStatus {
		return common.QuotaStatus{Supported: true, FSType: "xfs"}
	}
	t.Cleanup(func() { mountQuotaSupportFn = orig })
}

func TestSetFolderQuotaConcurrentDoesNotCorruptMapping(t *testing.T) {
	setupQuotaTestFiles(t)
	stubQuotaSudoExecSuccess(t)
	stubMountQuotaSupported(t)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			folder := fmt.Sprintf("folder%d", i)
			errs[i] = setFolderQuota("/mnt/pool1", "/mnt/pool1/"+folder, "pool1", folder, 10)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("setFolderQuota[%d] returned error: %v", i, err)
		}
	}

	// 每个文件夹必须有且仅有一个唯一的 projectID，且 /etc/projects 里的 path
	// 必须和 /etc/projid 里登记的 name 一一对应，不能出现重复 ID 或串号。
	projidData, err := os.ReadFile(filepath.Join(filepath.Dir(quotaProjidFile), "projid"))
	if err != nil {
		t.Fatalf("read projid: %v", err)
	}
	idToName := map[int]string{}
	nameToID := map[string]int{}
	for _, line := range strings.Split(string(projidData), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) != 2 {
			continue
		}
		id, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		if existing, dup := idToName[id]; dup && existing != parts[0] {
			t.Fatalf("projectID %d assigned to both %q and %q (race corrupted mapping)", id, existing, parts[0])
		}
		idToName[id] = parts[0]
		nameToID[parts[0]] = id
	}
	if len(nameToID) != n {
		t.Fatalf("expected %d distinct project names in projid, got %d: %v", n, len(nameToID), nameToID)
	}

	projectsData, err := os.ReadFile(filepath.Join(filepath.Dir(quotaProjectsFile), "projects"))
	if err != nil {
		t.Fatalf("read projects: %v", err)
	}
	idToPath := map[int]string{}
	for _, line := range strings.Split(string(projectsData), "\n") {
		id, path, _, ok := common.ParseProjectsLine(line)
		if !ok {
			continue
		}
		if existing, dup := idToPath[id]; dup && existing != path {
			t.Fatalf("projectID %d mapped to both path %q and %q (race corrupted /etc/projects)", id, existing, path)
		}
		idToPath[id] = path
	}
	if len(idToPath) != n {
		t.Fatalf("expected %d distinct project paths in projects, got %d: %v", n, len(idToPath), idToPath)
	}

	// Cross-check: for every folder's assigned projectID, the path recorded in
	// /etc/projects must match that exact folder (no cross-assignment).
	for i := 0; i < n; i++ {
		folder := fmt.Sprintf("folder%d", i)
		projName := projectName("pool1", folder)
		id, ok := nameToID[projName]
		if !ok {
			t.Fatalf("project %q missing from projid", projName)
		}
		wantPath := "/mnt/pool1/" + folder
		if gotPath := idToPath[id]; gotPath != wantPath {
			t.Fatalf("project %q (id %d): expected path %q, got %q", projName, id, wantPath, gotPath)
		}
	}
}

func TestAppendToFilePropagatesWriteError(t *testing.T) {
	setupQuotaTestFiles(t)

	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		return fmt.Errorf("disk full")
	}
	t.Cleanup(func() { safeWriteFileFn = orig })

	if err := appendToFile(quotaProjectsFile, "1000:/mnt/pool1/foo\n"); err == nil {
		t.Fatal("expected appendToFile to propagate write failure, got nil error")
	}
}

func TestSetFolderQuotaPropagatesAppendFailure(t *testing.T) {
	setupQuotaTestFiles(t)
	stubQuotaSudoExecSuccess(t)
	stubMountQuotaSupported(t)

	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		return fmt.Errorf("simulated write failure")
	}
	t.Cleanup(func() { safeWriteFileFn = orig })

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 10)
	if err == nil {
		t.Fatal("expected setFolderQuota to fail when projects/projid write fails, got nil")
	}
}

func TestRemoveProjectEntryPropagatesWriteError(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)

	// Pre-populate both files so removeProjectEntry has something to rewrite.
	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projidPath, []byte("pool1_foo:1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		return fmt.Errorf("simulated write failure")
	}
	t.Cleanup(func() { safeWriteFileFn = orig })

	if err := removeProjectEntry("pool1_foo"); err == nil {
		t.Fatal("expected removeProjectEntry to propagate write failure, got nil")
	}
}

func TestRemoveProjectEntrySuccessRemovesBothFiles(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)

	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/foo\n1001:/mnt/pool1/bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projidPath, []byte("pool1_foo:1000\npool1_bar:1001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeProjectEntry("pool1_foo"); err != nil {
		t.Fatalf("removeProjectEntry failed: %v", err)
	}

	projectsData, _ := os.ReadFile(projectsPath)
	if strings.Contains(string(projectsData), "/mnt/pool1/foo") {
		t.Errorf("expected /etc/projects entry for foo removed, got: %q", projectsData)
	}
	if !strings.Contains(string(projectsData), "/mnt/pool1/bar") {
		t.Errorf("expected /etc/projects entry for bar preserved, got: %q", projectsData)
	}

	projidData, _ := os.ReadFile(projidPath)
	if strings.Contains(string(projidData), "pool1_foo") {
		t.Errorf("expected /etc/projid entry for pool1_foo removed, got: %q", projidData)
	}
	if !strings.Contains(string(projidData), "pool1_bar") {
		t.Errorf("expected /etc/projid entry for pool1_bar preserved, got: %q", projidData)
	}
}

func TestUpdateProjectPathPropagatesWriteError(t *testing.T) {
	projectsPath, _ := setupQuotaTestFiles(t)

	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		return fmt.Errorf("simulated write failure")
	}
	t.Cleanup(func() { safeWriteFileFn = orig })

	if err := updateProjectPath("pool1_foo", 1000, "/mnt/pool1/new"); err == nil {
		t.Fatal("expected updateProjectPath to propagate write failure, got nil")
	}
}

func TestUpdateProjectPathSuccessRewritesPath(t *testing.T) {
	projectsPath, _ := setupQuotaTestFiles(t)

	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := updateProjectPath("pool1_foo", 1000, "/mnt/pool1/new"); err != nil {
		t.Fatalf("updateProjectPath failed: %v", err)
	}

	data, _ := os.ReadFile(projectsPath)
	if !strings.Contains(string(data), "/mnt/pool1/new") {
		t.Errorf("expected updated path in /etc/projects, got: %q", data)
	}
	if strings.Contains(string(data), "/mnt/pool1/old") {
		t.Errorf("expected old path removed from /etc/projects, got: %q", data)
	}
}

// ═══════════════════════════════════════════════════
// Item D 回归测试：setFolderQuota 新建映射路径中任一步骤失败都必须回滚前面
// 已完成的步骤，不能留下"/etc/projects 有映射但 projid 没有"、"project 已在
// 目录上生效但没有 limit（等于无限制）"这类半成品状态。
// ═══════════════════════════════════════════════════

// stubXfsQuotaFailingOn 让 sudoExecFn 只在命令参数中包含给定子串（如
// "project -s"、"limit -p"）时返回失败，其余调用（包括回滚用的
// "project -C"）照常成功，用于精确模拟 xfs_quota 某一步骤失败。
func stubXfsQuotaFailingOn(t *testing.T, failSubstr string) *[]string {
	t.Helper()
	var calls []string
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		calls = append(calls, joined)
		if strings.Contains(joined, failSubstr) {
			return "xfs_quota: simulated failure", fmt.Errorf("simulated xfs_quota failure")
		}
		return "", nil
	}
	t.Cleanup(func() { sudoExecFn = orig })
	return &calls
}

// TestSetFolderQuotaProjectSetupFailureRollsBackNewMapping 验证新建映射场景
// 下 xfs_quota "project -s" 失败时：(1) 返回错误；(2) 刚写入的 /etc/projects、
// /etc/projid 条目被回滚删除，不留下"文件里有映射但系统上从未生效"的孤儿。
func TestSetFolderQuotaProjectSetupFailureRollsBackNewMapping(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)
	stubMountQuotaSupported(t)
	stubXfsQuotaFailingOn(t, "project -s")

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 10)
	if err == nil {
		t.Fatal("expected setFolderQuota to fail when xfs_quota project -s fails, got nil")
	}

	projectsData, _ := os.ReadFile(projectsPath)
	if strings.Contains(string(projectsData), "/mnt/pool1/foo") {
		t.Errorf("projects = %q, want 回滚：project -s 失败后 /etc/projects 不应残留刚写入的条目", projectsData)
	}
	projidData, _ := os.ReadFile(projidPath)
	if strings.Contains(string(projidData), "pool1_foo") {
		t.Errorf("projid = %q, want 回滚：project -s 失败后 /etc/projid 不应残留刚写入的条目", projidData)
	}
}

// TestSetFolderQuotaLimitFailureRollsBackProjectAndMapping 验证新建映射场景
// 下 "project -s" 成功但 "limit -p" 失败时：(1) 返回错误；(2) 通过
// "project -C" 清掉刚生效的 project，不留下"已标记配额但实际无限制"的危险
// 半成品；(3) 刚写入的 /etc/projects、/etc/projid 条目也被回滚。
func TestSetFolderQuotaLimitFailureRollsBackProjectAndMapping(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)
	stubMountQuotaSupported(t)
	calls := stubXfsQuotaFailingOn(t, "limit -p")

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 10)
	if err == nil {
		t.Fatal("expected setFolderQuota to fail when xfs_quota limit -p fails, got nil")
	}

	var sawProjectClear bool
	for _, c := range *calls {
		if strings.Contains(c, "project -C pool1_foo") {
			sawProjectClear = true
		}
	}
	if !sawProjectClear {
		t.Errorf("calls = %v, want limit 设置失败后调用 project -C 回滚刚生效的 project", *calls)
	}

	projectsData, _ := os.ReadFile(projectsPath)
	if strings.Contains(string(projectsData), "/mnt/pool1/foo") {
		t.Errorf("projects = %q, want 回滚：limit 失败后 /etc/projects 不应残留刚写入的条目", projectsData)
	}
	projidData, _ := os.ReadFile(projidPath)
	if strings.Contains(string(projidData), "pool1_foo") {
		t.Errorf("projid = %q, want 回滚：limit 失败后 /etc/projid 不应残留刚写入的条目", projidData)
	}
}

// TestSetFolderQuotaAppendProjidFailureRollsBackProjectsEntry 验证新建映射
// 场景下 /etc/projects 写入成功但 /etc/projid 写入失败时：(1) 返回错误；
// (2) 刚写入的 /etc/projects 条目被回滚删除，不留下"projects 里有映射但
// projid 里找不到名字"的孤儿条目。
func TestSetFolderQuotaAppendProjidFailureRollsBackProjectsEntry(t *testing.T) {
	projectsPath, _ := setupQuotaTestFiles(t)
	stubQuotaSudoExecSuccess(t)
	stubMountQuotaSupported(t)

	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		if path == quotaProjidFile {
			return fmt.Errorf("simulated projid write failure")
		}
		return os.WriteFile(path, []byte(content), 0o644)
	}
	t.Cleanup(func() { safeWriteFileFn = orig })

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 10)
	if err == nil {
		t.Fatal("expected setFolderQuota to fail when /etc/projid append fails, got nil")
	}

	projectsData, _ := os.ReadFile(projectsPath)
	if strings.Contains(string(projectsData), "/mnt/pool1/foo") {
		t.Errorf("projects = %q, want 回滚：projid 写入失败后 /etc/projects 不应残留孤儿条目", projectsData)
	}
}

// TestSetFolderQuotaExistingMappingProjectFailureDoesNotRollBackPathUpdate
// 验证"更新已存在映射"场景（projID 已存在，只是改 path）下 project -s 失败
// 时不回滚 updateProjectPath 已经写入的新路径——因为旧的 project 配置本来就
// 存在，回滚路径反而会把 /etc/projects 的 path 改回旧值，和实际目录状态
// （folderPath 已变）不一致，所以这里只报告失败，不做路径回滚。
func TestSetFolderQuotaExistingMappingProjectFailureDoesNotRollBackPathUpdate(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)
	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projidPath, []byte("pool1_foo:1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubMountQuotaSupported(t)
	stubXfsQuotaFailingOn(t, "project -s")

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/new", "pool1", "foo", 10)
	if err == nil {
		t.Fatal("expected setFolderQuota to fail when xfs_quota project -s fails, got nil")
	}

	projectsData, _ := os.ReadFile(projectsPath)
	if !strings.Contains(string(projectsData), "/mnt/pool1/new") {
		t.Errorf("projects = %q, want 已存在映射场景下路径更新不被回滚（旧 project 配置仍然有效）", projectsData)
	}
}

func TestSetFolderQuotaRemoveZeroPropagatesRemoveFailure(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)
	stubQuotaSudoExecSuccess(t)

	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projidPath, []byte("pool1_foo:1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := safeWriteFileFn
	safeWriteFileFn = func(path, content string) error {
		return fmt.Errorf("simulated write failure")
	}
	t.Cleanup(func() { safeWriteFileFn = orig })

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 0)
	if err == nil {
		t.Fatal("expected setFolderQuota(quotaGB=0) to propagate removeProjectEntry failure, got nil")
	}
}

// ═══════════════════════════════════════════════════
// Item 4 回归测试：删除配额时必须先 project -C（此时映射还健在，xfs_quota
// 才能把 projName 解析到 project ID），再删 /etc/projects、/etc/projid 映射；
// 不能反过来——否则映射删除之后 xfs_quota 已经无法把 projName 解析成任何
// project ID，project -C 清不掉文件系统上残留的配额。
// ═══════════════════════════════════════════════════

// TestSetFolderQuotaRemoveCallsProjectClearBeforeDeletingMapping 验证
// setFolderQuota(quotaGB=0) 调用 "project -C" 时传入的 projName 在
// /etc/projects、/etc/projid 里仍然可以被解析到——即 project -C 发生在
// 映射删除之前，不是之后。用一个在 sudoExecFn 里内联检查映射文件当前内容
// 的 fake 来确认这一点(如果顺序反了，调用 project -C 时 findProjectID 会
// 返回 -1，因为映射已经被删掉)。
func TestSetFolderQuotaRemoveCallsProjectClearBeforeDeletingMapping(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)

	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projidPath, []byte("pool1_foo:1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var projectIDAtProjectCTime int
	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		if strings.Contains(joined, "project -C") {
			// 调用 project -C 这一刻,映射必须还没被删——用 findProjectID
			// 现场查一次,确认此时仍能解析到 projID,而不是已经变成 -1。
			projectIDAtProjectCTime = findProjectID("pool1_foo")
		}
		return "", nil
	}
	t.Cleanup(func() { sudoExecFn = orig })

	if err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 0); err != nil {
		t.Fatalf("setFolderQuota(quotaGB=0) 失败: %v", err)
	}

	if projectIDAtProjectCTime != 1000 {
		t.Fatalf("project -C 执行时 findProjectID(pool1_foo) = %d, want 1000"+
			"(说明 project -C 必须在映射删除之前调用,此时 projName 还能被正常解析到 project ID)",
			projectIDAtProjectCTime)
	}

	// 清理路径最终也要把映射删干净。
	projectsData, _ := os.ReadFile(projectsPath)
	if strings.Contains(string(projectsData), "/mnt/pool1/foo") {
		t.Errorf("projects = %q, want 清除成功后映射被删除", projectsData)
	}
	projidData, _ := os.ReadFile(projidPath)
	if strings.Contains(string(projidData), "pool1_foo") {
		t.Errorf("projid = %q, want 清除成功后映射被删除", projidData)
	}
}

// TestSetFolderQuotaRemoveProjectClearFailureDoesNotDeleteMapping 验证
// "project -C" 本身失败(如 xfs_quota 调用出错)时,setFolderQuota 必须
// 直接返回错误,且不得继续删除 /etc/projects、/etc/projid 映射——否则会
// 出现"映射已经没了,但文件系统上旧的 project 配额其实还生效"的危险不一致
// (旧实现的顺序 bug 复现场景的反向验证:先删映射会让 project -C 失去
// 解析依据;这里验证的是 project -C 失败时,正确顺序下映射必须原样保留,
// 不能被继续执行到的删除逻辑误删)。
func TestSetFolderQuotaRemoveProjectClearFailureDoesNotDeleteMapping(t *testing.T) {
	projectsPath, projidPath := setupQuotaTestFiles(t)

	if err := os.WriteFile(projectsPath, []byte("1000:/mnt/pool1/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projidPath, []byte("pool1_foo:1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := sudoExecFn
	sudoExecFn = func(name string, args ...string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		if strings.Contains(joined, "project -C") {
			return "xfs_quota: simulated failure", fmt.Errorf("simulated project -C failure")
		}
		return "", nil
	}
	t.Cleanup(func() { sudoExecFn = orig })

	err := setFolderQuota("/mnt/pool1", "/mnt/pool1/foo", "pool1", "foo", 0)
	if err == nil {
		t.Fatal("expected setFolderQuota(quotaGB=0) to fail when xfs_quota project -C fails, got nil")
	}

	// 映射必须完全原样保留(还原到调用前),不能被部分删除——方便安全重试。
	projectsData, _ := os.ReadFile(projectsPath)
	if !strings.Contains(string(projectsData), "1000:/mnt/pool1/foo") {
		t.Errorf("projects = %q, want project -C 失败后映射完全原样保留(可安全重试)", projectsData)
	}
	projidData, _ := os.ReadFile(projidPath)
	if !strings.Contains(string(projidData), "pool1_foo:1000") {
		t.Errorf("projid = %q, want project -C 失败后映射完全原样保留(可安全重试)", projidData)
	}
}
