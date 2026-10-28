package diskmgmt

import (
	"testing"
)

// TestParseMdstatBlocks_CleanRAID1 验证干净阵列（无进度行）的基本解析：
// 成员 token、状态行、头行字段都正确切出，Progress 为空。
func TestParseMdstatBlocks_CleanRAID1(t *testing.T) {
	content := `Personalities : [raid1]
md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if b.MdName != "md0" {
		t.Errorf("MdName = %q, want md0", b.MdName)
	}
	if len(b.MemberTokens) != 2 {
		t.Fatalf("got %d member tokens, want 2: %v", len(b.MemberTokens), b.MemberTokens)
	}
	if b.StatusLine != "[UU]" {
		t.Errorf("StatusLine = %q, want [UU]", b.StatusLine)
	}
	if len(b.Progress) != 0 {
		t.Errorf("got %d progress lines, want 0: %v", len(b.Progress), b.Progress)
	}
}

// TestParseMdstatBlocks_RAID1Resyncing 验证进度行在 blocks 行之后（真实 mdstat
// 的常见布局），且百分比必须从 "]" 之后提取（consensus P0-D 核心修复点：
// 此前 extractPercent(line[:idx]) 在 "]" 之前找，真实百分比在 "]" 之后）。
func TestParseMdstatBlocks_RAID1Resyncing(t *testing.T) {
	content := `md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]
      [=>...................]  resync = 15.2% (298560640/1953382464) finish=17.0min speed=162144K/sec

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	p, ok := firstProgress(blocks[0])
	if !ok {
		t.Fatal("应该解析出一条进度行")
	}
	if p.Kind != "resync" {
		t.Errorf("Kind = %q, want resync", p.Kind)
	}
	if p.Percent != "15.2%" {
		t.Errorf("Percent = %q, want 15.2%%", p.Percent)
	}
	if p.Speed != "162144K/sec" {
		t.Errorf("Speed = %q, want 162144K/sec", p.Speed)
	}
	if p.Finish != "17.0min" {
		t.Errorf("Finish = %q, want 17.0min", p.Finish)
	}
}

// TestParseMdstatBlocks_RAID5ReshapeWithBackupFile 验证 raid5 reshape 场景，
// 含一条 bitmap/backup-file 式的额外行，不应被误判为进度行或污染成员 token。
func TestParseMdstatBlocks_RAID5ReshapeWithBackupFile(t *testing.T) {
	content := `md0 : active raid5 sdc[3] sdb[1] sda[0]
      3906764800 blocks super 1.2 level 5, 512k chunk, algorithm 2 [4/3] [UUU_]
      [>....................]  reshape =  3.9% (76000000/976691200) finish=194.5min speed=41226K/sec
      bitmap: 0/8 pages [0KB], 65536KB chunk

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	b := blocks[0]
	if len(b.MemberTokens) != 3 {
		t.Fatalf("got %d member tokens, want 3: %v", len(b.MemberTokens), b.MemberTokens)
	}
	p, ok := firstProgress(b)
	if !ok || p.Kind != "reshape" {
		t.Fatalf("应该解析出一条 reshape 进度行, got %+v ok=%v", p, ok)
	}
	if p.Percent != "3.9%" {
		t.Errorf("Percent = %q, want 3.9%%", p.Percent)
	}
	if p.Speed != "41226K/sec" {
		t.Errorf("Speed = %q, want 41226K/sec", p.Speed)
	}
}

// TestParseMdstatBlocks_DegradedWithFailedDisk 验证含 (F) 失败成员的降级阵列：
// (F) 必须被识别为 failed 标志（memberFlag），不与 (S) spare 混淆。
func TestParseMdstatBlocks_DegradedWithFailedDisk(t *testing.T) {
	content := `md0 : active raid1 sdb[1](F) sda[0]
      1953511424 blocks super 1.2 [2/1] [U_]

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	b := blocks[0]
	if len(b.MemberTokens) != 2 {
		t.Fatalf("got %d member tokens, want 2: %v", len(b.MemberTokens), b.MemberTokens)
	}
	flags := map[string]int{}
	for _, tok := range b.MemberTokens {
		flags[memberFlag(tok)]++
	}
	if flags["F"] != 1 {
		t.Errorf("got %d failed members, want 1, flags=%v tokens=%v", flags["F"], flags, b.MemberTokens)
	}
	if flags[""] != 1 {
		t.Errorf("got %d normal active members, want 1, flags=%v", flags[""], flags)
	}
}

// TestParseMdstatBlocks_WithSpare 验证含 (S) 真 spare 成员的阵列：spare 标志
// 必须与 failed/active 区分开，不计入任何其他桶。
func TestParseMdstatBlocks_WithSpare(t *testing.T) {
	content := `md0 : active raid1 sdc[2](S) sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	b := blocks[0]
	if len(b.MemberTokens) != 3 {
		t.Fatalf("got %d member tokens, want 3: %v", len(b.MemberTokens), b.MemberTokens)
	}
	flags := map[string]int{}
	for _, tok := range b.MemberTokens {
		flags[memberFlag(tok)]++
	}
	if flags["S"] != 1 {
		t.Errorf("got %d spare members, want 1, flags=%v", flags["S"], flags)
	}
	if flags[""] != 2 {
		t.Errorf("got %d normal active members, want 2, flags=%v", flags[""], flags)
	}
}

// TestParseMdstatBlocks_CheckRepair 验证 check/repair 场景下进度行能被正确
// 识别为对应 Kind，且百分比仍从 "]" 之后解析。
func TestParseMdstatBlocks_CheckRepair(t *testing.T) {
	content := `md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]
      [==>..................]  check = 12.0% (234567890/1953382464) finish=25.3min speed=98765K/sec

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	p, ok := firstProgress(blocks[0])
	if !ok || p.Kind != "check" {
		t.Fatalf("应该解析出 check 进度行, got %+v ok=%v", p, ok)
	}
	if p.Percent != "12.0%" {
		t.Errorf("Percent = %q, want 12.0%%", p.Percent)
	}
}

// TestParseMdstatBlocks_MultiArrayIsolation 验证多阵列并存时，其他阵列的进度
// 行不得污染本阵列判定——这是 consensus P0-D 要求的关键隔离用例。
func TestParseMdstatBlocks_MultiArrayIsolation(t *testing.T) {
	content := `md1 : active raid1 sdd[1] sdc[0]
      976630464 blocks super 1.2 [2/2] [UU]
      [=>...................]  resync = 42.0% (410000000/976630464) finish=5.0min speed=90000K/sec

md0 : active raid1 sdb[1] sda[0]
      1953511424 blocks super 1.2 [2/2] [UU]

unused devices: <none>
`
	blocks := parseMdstatBlocks(content)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}

	md0, ok := findBlockByMdName(blocks, "md0")
	if !ok {
		t.Fatal("找不到 md0 块")
	}
	if blockHasProgress(md0) {
		t.Errorf("md0 自身没有 resync，不应被 md1 的进度行污染: %+v", md0.Progress)
	}

	md1, ok := findBlockByMdName(blocks, "md1")
	if !ok {
		t.Fatal("找不到 md1 块")
	}
	if !blockHasProgress(md1) {
		t.Error("md1 应该有 resync 进度行")
	}
	p, _ := firstProgress(md1)
	if p.Percent != "42.0%" {
		t.Errorf("md1 Percent = %q, want 42.0%%", p.Percent)
	}
}

// TestParseMdstatBlocks_Empty 验证空输入/无阵列输入不 panic，返回空切片。
func TestParseMdstatBlocks_Empty(t *testing.T) {
	if got := parseMdstatBlocks(""); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if got := parseMdstatBlocks("Personalities : \nunused devices: <none>\n"); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestMemberFlag 覆盖 F/S/W/正常四种成员标志解析。
func TestMemberFlag(t *testing.T) {
	cases := []struct {
		token string
		want  string
	}{
		{"sda[0]", ""},
		{"sdb[1](F)", "F"},
		{"sdc[2](S)", "S"},
		{"sdd[3](W)", "W"},
	}
	for _, c := range cases {
		if got := memberFlag(c.token); got != c.want {
			t.Errorf("memberFlag(%q) = %q, want %q", c.token, got, c.want)
		}
	}
}

// TestFindBlockByMdName 验证找到/找不到两种路径。
func TestFindBlockByMdName(t *testing.T) {
	blocks := []MdstatBlock{{MdName: "md0"}, {MdName: "md1"}}
	if _, ok := findBlockByMdName(blocks, "md0"); !ok {
		t.Error("应该找到 md0")
	}
	if _, ok := findBlockByMdName(blocks, "md5"); ok {
		t.Error("不应该找到 md5")
	}
}
