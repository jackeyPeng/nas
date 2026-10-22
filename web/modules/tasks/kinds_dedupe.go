package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nas-panel/modules/filemgmt"
)

// photoDedupeKind 是本任务 kind 的字符串常量（F1 第一批任务 kind 之一，
// spec v4 §四）。
const photoDedupeKind = "photo_dedupe"

// dedupeHeadHashBytes 是两级哈希第一级读取的头部字节数：按 size 分桶后，
// 先比较头 64KB 的哈希，相同者才进入全量哈希（避免大文件全量重复读取）。
const dedupeHeadHashBytes = 64 * 1024

// dedupeReadBufferBytes 是读文件计算哈希用的缓冲区大小。
const dedupeReadBufferBytes = 32 * 1024

// dedupeExtWhitelist 是 photo_dedupe 扫描的扩展名白名单（小写比较）。
var dedupeExtWhitelist = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true,
	".gif": true, ".webp": true, ".bmp": true,
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true,
}

// recycleSegmentNames 是扫描时需要整段跳过的目录名（回收站/历史遗留
// 命名），对照 filemgmt 的 .recycle 既有布局 + 兼容 #recycle 命名。
var recycleSegmentNames = map[string]bool{".recycle": true, "#recycle": true}

func init() {
	RegisterOp("move_to_recycle", execMoveToRecycle)
	RegisterResultBuilder(photoDedupeKind, buildDedupeResult)
}

// dedupeCandidate 是扫描阶段收集到的候选文件（已过扩展名白名单/隐藏/
// 符号链接/.recycle 过滤，尚未分组）。
type dedupeCandidate struct {
	absPath string
	relPath string // 池内相对路径（Step.Args["src"] 用这个，不是绝对路径）
	size    int64
	modTime time.Time
}

// PlanPhotoDedupe 同步扫描池内（或 scopeRel 子树内）图片/视频重复文件，
// 生成 move_to_recycle 步骤计划；budget 内扫不完时返回已扫部分
// （partial=true）。已存在同 (kind,scopeRel) 的 pending_confirm job 时
// 直接返回它，不重扫（plan_task 防重复扫描）。
func PlanPhotoDedupe(scopeRel string, budget time.Duration) (Job, bool, error) {
	if existing, ok := FindPendingJob(photoDedupeKind, scopeRel); ok {
		return existing, false, nil
	}

	scopeAbs, err := filemgmt.ResolvePoolPath(scopeRel)
	if err != nil {
		return Job{}, false, fmt.Errorf("scope 非法: %w", err)
	}

	deadline := time.Now().Add(budget)
	candidates, partial, err := scanDedupeCandidates(scopeAbs, deadline)
	if err != nil {
		return Job{}, false, err
	}

	groups := groupDedupeCandidates(candidates)
	plan, impact := buildDedupePlan(groups)

	job, err := CreateJob(photoDedupeKind, scopeRel, plan, impact)
	if err != nil {
		return Job{}, false, err
	}
	return job, partial, nil
}

// scanDedupeCandidates 遍历 root 子树，收集过白名单/跳过规则的候选文件。
// deadline 到达时立即停止遍历并返回 partial=true（已收集到的候选不丢）。
func scanDedupeCandidates(root string, deadline time.Time) ([]dedupeCandidate, bool, error) {
	var candidates []dedupeCandidate
	partial := false

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			partial = true
			return filepath.SkipAll
		}

		name := d.Name()
		if d.IsDir() {
			if path != root && (recycleSegmentNames[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(name, ".") {
			return nil // 隐藏文件跳过
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !dedupeExtWhitelist[ext] {
			return nil
		}

		info, lerr := d.Info() // Lstat 口径：目录项的 Info() 不跟随符号链接
		if lerr != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // 符号链接跳过（Lstat 判定）
		}
		if !info.Mode().IsRegular() {
			return nil // 非普通文件跳过
		}

		candidates = append(candidates, dedupeCandidate{
			absPath: path,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		return nil, false, walkErr
	}

	// relPath 统一在这里换算：候选 absPath 必然已经在池根内（root 本身
	// 就来自 ResolvePoolPath 校验过），用 filemgmt 导出的池根工具函数
	// 换算相对路径，不在 tasks 包里重复猜测池根布局细节。
	for i := range candidates {
		relPath, err := filemgmt.RelPoolPath(candidates[i].absPath)
		if err != nil {
			continue
		}
		candidates[i].relPath = relPath
	}

	return candidates, partial, nil
}

// groupDedupeCandidates 按 size 分桶 → 桶内头 64KB 哈希分组 → 组内再按
// 全量哈希细分 → 全量哈希相同者为最终一组。单文件的组（无重复）不返回。
func groupDedupeCandidates(candidates []dedupeCandidate) [][]dedupeCandidate {
	bySize := map[int64][]dedupeCandidate{}
	for _, c := range candidates {
		bySize[c.size] = append(bySize[c.size], c)
	}

	var result [][]dedupeCandidate
	for _, bucket := range bySize {
		if len(bucket) < 2 {
			continue
		}
		byHead := map[string][]dedupeCandidate{}
		for _, c := range bucket {
			h, err := hashFilePrefix(c.absPath, dedupeHeadHashBytes)
			if err != nil {
				continue // 读失败的候选直接丢弃（不参与成组，不中断整体扫描）
			}
			byHead[h] = append(byHead[h], c)
		}
		for _, headGroup := range byHead {
			if len(headGroup) < 2 {
				continue
			}
			byFull := map[string][]dedupeCandidate{}
			for _, c := range headGroup {
				h, err := hashFilePrefix(c.absPath, -1)
				if err != nil {
					continue
				}
				byFull[h] = append(byFull[h], c)
			}
			for _, fullGroup := range byFull {
				if len(fullGroup) >= 2 {
					result = append(result, fullGroup)
				}
			}
		}
	}
	return result
}

// hashFilePrefix 计算 path 前 n 字节的 sha256（n<0 表示全量）。读取用
// 32KB 缓冲区。
func hashFilePrefix(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, dedupeReadBufferBytes)
	var reader io.Reader = f
	if n >= 0 {
		reader = io.LimitReader(f, n)
	}
	if _, err := io.CopyBuffer(h, reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildDedupePlan 对每组按保留策略（路径最短；并列取 mtime 最早）选出
// 保留者，其余生成 move_to_recycle 步骤；汇总 Impact。
func buildDedupePlan(groups [][]dedupeCandidate) ([]Step, Impact) {
	var plan []Step
	impact := Impact{}

	for _, group := range groups {
		keepIdx := pickKeepIndex(group)
		impact.Groups++
		for i, c := range group {
			if i == keepIdx {
				continue
			}
			impact.Files++
			impact.Bytes += c.size
			plan = append(plan, Step{
				Key:  "mv:" + c.absPath,
				Op:   "move_to_recycle",
				Args: map[string]string{"src": c.relPath},
			})
		}
	}

	// 步骤顺序稳定化（map 遍历顺序不确定，排序便于测试断言与用户查看）。
	sort.Slice(plan, func(i, j int) bool { return plan[i].Key < plan[j].Key })

	return plan, impact
}

// pickKeepIndex 返回组内应保留的候选索引：路径最短者；并列取 mtime 最早。
func pickKeepIndex(group []dedupeCandidate) int {
	best := 0
	for i := 1; i < len(group); i++ {
		if isBetterKeep(group[i], group[best]) {
			best = i
		}
	}
	return best
}

func isBetterKeep(a, b dedupeCandidate) bool {
	if len(a.relPath) != len(b.relPath) {
		return len(a.relPath) < len(b.relPath)
	}
	return a.modTime.Before(b.modTime)
}

// ---------------------------------------------------------------------------
// 执行链：move_to_recycle op 接线 + 本次执行统计（供 Result JSON 汇总）
// ---------------------------------------------------------------------------

// dedupeRunStats 记录单个任务一次 worker 执行期间的 moved/freed/skipped
// 统计（jobID -> 累计值）；只在内存里，任务跑完即可丢弃（Result 已落库）。
var (
	dedupeStatsMu sync.Mutex
	dedupeStats   = map[string]*dedupeStat{}
)

type dedupeStat struct {
	movedFiles int
	freedBytes int64
	skipped    int
}

func recordDedupeMoved(jobID string, freedBytes int64) {
	if jobID == "" {
		return
	}
	dedupeStatsMu.Lock()
	defer dedupeStatsMu.Unlock()
	s := dedupeStats[jobID]
	if s == nil {
		s = &dedupeStat{}
		dedupeStats[jobID] = s
	}
	s.movedFiles++
	s.freedBytes += freedBytes
}

func recordDedupeSkipped(jobID string) {
	if jobID == "" {
		return
	}
	dedupeStatsMu.Lock()
	defer dedupeStatsMu.Unlock()
	s := dedupeStats[jobID]
	if s == nil {
		s = &dedupeStat{}
		dedupeStats[jobID] = s
	}
	s.skipped++
}

// execMoveToRecycle 是 "move_to_recycle" 的真实执行体：
// Args["src"] → filemgmt.ResolvePoolPath → filemgmt.MoveToRecycle(src, "")。
//
// 幂等语义（F1 Task 4）：源已不存在时，是否已记入 checkpoint 由 worker 的
// 断点续传逻辑负责（已完成步骤 worker 不会再调用本函数）；本函数被调用
// 时若发现源已不存在，视为"未记但源已不在"（家庭环境文件可能被手动动
// 过），记 skipped 计数后返回 nil（不整任务失败，不中断后续步骤）。
func execMoveToRecycle(ctx context.Context, step Step) error {
	jobID, _ := ctx.Value(ctxJobIDKey).(string)

	src := step.Args["src"]
	srcAbs, err := filemgmt.ResolvePoolPath(src)
	if err != nil {
		return fmt.Errorf("move_to_recycle 路径非法: %w", err)
	}

	size := fileSizeOrZero(srcAbs)

	_, merr := filemgmt.MoveToRecycle(srcAbs, "")
	if merr != nil {
		if errors.Is(merr, filemgmt.ErrNotFound) {
			recordDedupeSkipped(jobID)
			return nil
		}
		return merr
	}

	recordDedupeMoved(jobID, size)
	return nil
}

func fileSizeOrZero(path string) int64 {
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// buildDedupeResult 是 photo_dedupe 的 ResultBuilder：把本次执行累计的
// moved/freed/skipped 序列化为 Result JSON；doneCount 作为兜底（没有
// 统计记录时——理论上不会发生，防御性保留）。
func buildDedupeResult(jobID string, doneCount int) string {
	dedupeStatsMu.Lock()
	s, ok := dedupeStats[jobID]
	if ok {
		delete(dedupeStats, jobID) // 任务已跑完，统计落库即可丢弃内存态
	}
	dedupeStatsMu.Unlock()

	if !ok {
		data, _ := json.Marshal(map[string]interface{}{"moved": doneCount, "freed": 0, "skipped": 0})
		return string(data)
	}
	data, _ := json.Marshal(map[string]interface{}{
		"moved": s.movedFiles, "freed": s.freedBytes, "skipped": s.skipped,
	})
	return string(data)
}
