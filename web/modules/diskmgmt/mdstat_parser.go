package diskmgmt

import (
	"regexp"
	"strings"
)

// ═══════════════════════════════════════════════════
// consensus P0-D（文本层）：mdstat 统一块解析
// ═══════════════════════════════════════════════════
//
// 根因（四处独立、互不一致的 /proc/mdstat 临时解析逐一实锤）：
//   - getRAIDStatus 把 (F) 失败成员计入 SpareDisks，(S) 真 spare 反而不计入任何桶；
//   - SyncPercent 用 extractPercent(line[:idx]) 在 "]" 之前找百分比——真实 mdstat
//     的百分比在 "]" 之后（形如 "[=>...]  resync = 15.2% (...) finish=... speed=..."）；
//   - handleRAIDExpandFS 只在 MD 头行本身找 reshape/resync 关键词——头行从不携带
//     这些词，闸门形同虚设；
//   - handleRAIDReshapeStatus/waitForResync 只看头行的下一行（lines[i+1]）——真实
//     进度行通常在 blocks 行之后（i+2 及以后），会漏检。
//
// 修法：统一按"行首非空白"切块（每个阵列一块：头行 + 后续缩进行），单独解析
// 一次，四个调用点都改走这个解析结果，不再各自临时拿字符串下标猜。

// MdstatProgressLine 一条进度行（resync/recovery/reshape/check/repair）。
type MdstatProgressLine struct {
	Kind    string // resync, recovery, reshape, check, repair
	Percent string // "15.2%"（含 % 号，空串表示未解析出百分比）
	Speed   string // "162144K/sec"
	Finish  string // "17.0min"
}

// MdstatBlock 一个 md 阵列块的解析结果。
type MdstatBlock struct {
	MdName       string   // "md0"（不带 /dev/ 前缀）
	HeaderFields []string // 头行按空格切分的全部字段（含 "active"/"raid1"/成员 token 等）
	MemberTokens []string // 成员 token 原文，如 "sdb[1]" "sda[0](F)" "sdc[2](S)"
	StatusLine   string   // "[UU]" / "[_U]" 等阵列状态行（无则空串）
	Progress     []MdstatProgressLine
}

var progressKindRe = regexp.MustCompile(`\b(resync|recovery|reshape|check|repair)\b`)
var percentAfterBracketRe = regexp.MustCompile(`=\s*([0-9.]+)%`)
var speedRe = regexp.MustCompile(`speed=(\S+)`)
var finishRe = regexp.MustCompile(`finish=(\S+)`)
var statusLineRe = regexp.MustCompile(`^\[[_U]+\]$`)
var memberTokenRe = regexp.MustCompile(`^[a-zA-Z0-9]+\[\d+\]`)

// parseMdstatBlocks 按"行首非空白"切块解析 /proc/mdstat 全文，每个阵列输出一块。
// 纯函数，不发起任何系统命令，便于单测注入 fixture。
func parseMdstatBlocks(content string) []MdstatBlock {
	var blocks []MdstatBlock
	var current *MdstatBlock

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		isHeaderLine := line[0] != ' ' && line[0] != '\t'
		if isHeaderLine {
			// 头行形如: "md0 : active raid1 sdb[1] sda[0]"
			// "Personalities : ..." 和 "unused devices: <none>" 也是顶格行，
			// 但不是阵列块——只有匹配 "^md\d+\s*:" 的头行才开新块。
			fields := strings.Fields(line)
			if current != nil {
				blocks = append(blocks, *current)
				current = nil
			}
			if len(fields) < 2 || !mdNameRe.MatchString(fields[0]) || fields[1] != ":" {
				continue
			}
			current = &MdstatBlock{MdName: fields[0], HeaderFields: fields}
			for _, f := range fields[3:] {
				if memberTokenRe.MatchString(f) {
					current.MemberTokens = append(current.MemberTokens, f)
				}
			}
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// 阵列状态行，如 "1953511424 blocks super 1.2 [2/2] [UU]"——取行内最后一个
		// 形如 [UU]/[_U] 的方括号 token。
		for _, tok := range strings.Fields(trimmed) {
			if statusLineRe.MatchString(tok) {
				current.StatusLine = tok
			}
		}
		// 进度行：含 resync/recovery/reshape/check/repair 关键词。
		if m := progressKindRe.FindString(trimmed); m != "" {
			pl := MdstatProgressLine{Kind: m}
			if idx := strings.Index(trimmed, "]"); idx >= 0 {
				if pm := percentAfterBracketRe.FindStringSubmatch(trimmed[idx+1:]); len(pm) >= 2 {
					pl.Percent = pm[1] + "%"
				}
			} else if pm := percentAfterBracketRe.FindStringSubmatch(trimmed); len(pm) >= 2 {
				// 没有进度条 "[...]" 的行（如 check/repair 有时不带条形图），
				// 直接在整行找 "= xx%"。
				pl.Percent = pm[1] + "%"
			}
			if sm := speedRe.FindStringSubmatch(trimmed); len(sm) >= 2 {
				pl.Speed = sm[1]
			}
			if fm := finishRe.FindStringSubmatch(trimmed); len(fm) >= 2 {
				pl.Finish = fm[1]
			}
			current.Progress = append(current.Progress, pl)
		}
	}
	if current != nil {
		blocks = append(blocks, *current)
	}
	return blocks
}

var mdNameRe = regexp.MustCompile(`^md\d+$`)

// memberFlag 返回成员 token 的标志位："F"（failed）、"S"（spare）、或 ""（正常 active）。
func memberFlag(token string) string {
	if strings.Contains(token, "(F)") {
		return "F"
	}
	if strings.Contains(token, "(S)") {
		return "S"
	}
	if strings.Contains(token, "(W)") {
		return "W"
	}
	return ""
}

// findBlockByMdName 在解析结果中查找指定 md 设备名（不带 /dev/ 前缀）对应的块。
func findBlockByMdName(blocks []MdstatBlock, mdName string) (MdstatBlock, bool) {
	for _, b := range blocks {
		if b.MdName == mdName {
			return b, true
		}
	}
	return MdstatBlock{}, false
}

// blockHasProgress 判断该阵列块内是否存在任一进度行（resync/recovery/reshape/
// check/repair），供 handleRAIDExpandFS 的扩容闸门使用——此前错误地在头行本身
// 找这些关键词，头行从不携带，闸门形同虚设。
func blockHasProgress(b MdstatBlock) bool {
	return len(b.Progress) > 0
}

// firstProgress 返回块内第一条进度行（通常也是唯一一条），供
// handleRAIDReshapeStatus/waitForResync 使用。ok=false 表示无进度行。
func firstProgress(b MdstatBlock) (MdstatProgressLine, bool) {
	if len(b.Progress) == 0 {
		return MdstatProgressLine{}, false
	}
	return b.Progress[0], true
}
