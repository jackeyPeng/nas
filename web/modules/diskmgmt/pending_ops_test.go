package diskmgmt

import "testing"

// TestMergeFolderUpdate 回归：存储页文件夹权限对话框（不携带 write_users）
// 不得清空矩阵已配置的按用户元数据，否则只读用户静默获得写权限。
func TestMergeFolderUpdate(t *testing.T) {
	meta := func(valid, write string) *FolderMeta {
		return &FolderMeta{ValidUsers: valid, WriteUsers: write}
	}

	cases := []struct {
		name      string
		existing  *FolderMeta
		op        PendingOp
		wantPerm  string
		wantValid string
		wantWrite string
	}{
		{
			name:      "改回收站/权限不清空 write_users",
			existing:  meta("alice,bob", "alice"),
			op:        PendingOp{Permission: "readonly", ValidUsers: ""},
			wantPerm:  "readwrite", // write 非空 → 反向同步（与矩阵路径一致）
			wantValid: "alice,bob",
			wantWrite: "alice",
		},
		{
			name:      "对话框显式收窄 valid → write 裁剪保持子集不变式",
			existing:  meta("alice,bob", "alice,bob"),
			op:        PendingOp{Permission: "readwrite", ValidUsers: "alice"},
			wantPerm:  "readwrite",
			wantValid: "alice",
			wantWrite: "alice",
		},
		{
			name:      "收窄后 write 全被裁掉 → 按 op.permission 落地",
			existing:  meta("alice,bob", "bob"),
			op:        PendingOp{Permission: "readonly", ValidUsers: "alice"},
			wantPerm:  "readonly",
			wantValid: "alice",
			wantWrite: "",
		},
		{
			name:      "noaccess 保持 noaccess（write 保留但 SMB 段不生成）",
			existing:  meta("alice", "alice"),
			op:        PendingOp{Permission: "noaccess", ValidUsers: ""},
			wantPerm:  "noaccess",
			wantValid: "alice",
			wantWrite: "alice",
		},
		{
			name:      "valid 被显式清空 → write 一并清空（开放共享/禁用语义）",
			existing:  meta("alice", "alice"),
			op:        PendingOp{Permission: "readwrite", ValidUsers: ""},
			wantPerm:  "readwrite",
			wantValid: "alice", // op 空 → 继承现有，而非清空
			wantWrite: "alice",
		},
		{
			name:      "无现有元数据（新文件夹）→ 原样落地",
			existing:  nil,
			op:        PendingOp{Permission: "readwrite", ValidUsers: "carol"},
			wantPerm:  "readwrite",
			wantValid: "carol",
			wantWrite: "",
		},
		{
			name:      "旧文件夹级数据（无按用户元数据）→ 行为不变",
			existing:  meta("", ""),
			op:        PendingOp{Permission: "readonly", ValidUsers: ""},
			wantPerm:  "readonly",
			wantValid: "",
			wantWrite: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perm, valid, write := mergeFolderUpdate(tc.existing, tc.op)
			if perm != tc.wantPerm || valid != tc.wantValid || write != tc.wantWrite {
				t.Errorf("mergeFolderUpdate = (%q,%q,%q), want (%q,%q,%q)",
					perm, valid, write, tc.wantPerm, tc.wantValid, tc.wantWrite)
			}
		})
	}
}

func TestIntersectUserList(t *testing.T) {
	cases := []struct{ keep, allowed, want string }{
		{"alice,bob", "bob,carol", "bob"},
		{"alice", "alice", "alice"},
		{"", "alice", ""},
		{"alice", "", ""},
		{" alice , bob ,bob", "bob, alice", "alice,bob"}, // 去空格去重保序
	}
	for _, tc := range cases {
		if got := intersectUserList(tc.keep, tc.allowed); got != tc.want {
			t.Errorf("intersectUserList(%q,%q) = %q, want %q", tc.keep, tc.allowed, got, tc.want)
		}
	}
}
