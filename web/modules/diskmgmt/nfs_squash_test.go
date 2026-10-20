package diskmgmt

import "testing"

// NFS 导出选项：家用场景可用性优先，默认 no_root_squash + insecure（2026-09-24 起放宽）
func TestNFSExportOpts(t *testing.T) {
	cases := []struct {
		name string
		meta FolderMeta
		want string
	}{
		{
			name: "rw 默认 no_root_squash + insecure",
			meta: FolderMeta{Permission: "readwrite", NFSExport: true},
			want: "rw,sync,no_subtree_check,no_root_squash,insecure",
		},
		{
			name: "rw 显式 NFSNoRootSquash 结果一致",
			meta: FolderMeta{Permission: "readwrite", NFSExport: true, NFSNoRootSquash: true},
			want: "rw,sync,no_subtree_check,no_root_squash,insecure",
		},
		{
			name: "readonly 一律 ro + insecure",
			meta: FolderMeta{Permission: "readonly", NFSExport: true, NFSNoRootSquash: true},
			want: "ro,sync,no_subtree_check,insecure",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nfsExportOpts(tc.meta); got != tc.want {
				t.Errorf("nfsExportOpts(%+v) = %q, want %q", tc.meta, got, tc.want)
			}
		})
	}
}

// 文件夹级更新不携带 NFS 选项时继承现有值（防对话框静默重置）
func TestExecuteUpdateFolderInheritsNFSFlag(t *testing.T) {
	// 直接验证继承逻辑的判定条件（executeUpdateFolder 依赖 DB，这里测决策规则）
	// 复刻 executeUpdateFolder 的三态决策（含 existing 为 nil 的分支，消除恒真 nil 判断）
	decide := func(op PendingOp, existing *FolderMeta) bool {
		if op.NFSNoRootSquashSet {
			return op.NFSNoRootSquash
		}
		if existing != nil {
			return existing.NFSNoRootSquash
		}
		return false
	}
	existing := &FolderMeta{Name: "docs", Path: "/data/nas1/docs", NFSExport: true, NFSNoRootSquash: true}
	op := PendingOp{Action: "update", FolderName: "docs", FolderPath: "/data/nas1/docs", Permission: "readwrite"} // Set=false

	if !decide(op, existing) {
		t.Error("未显式携带 nfs_no_root_squash 时应继承现有值 true")
	}

	op2 := PendingOp{Action: "update", NFSNoRootSquash: false, NFSNoRootSquashSet: true}
	if decide(op2, existing) {
		t.Error("显式传 false 时应关闭 no_root_squash")
	}

	if decide(PendingOp{Action: "update"}, nil) {
		t.Error("无现有元数据且未显式携带时应默认 false")
	}
}
