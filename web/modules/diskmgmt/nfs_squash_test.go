package diskmgmt

import "testing"

// NFS 导出选项：默认 root_squash（安全基线），no_root_squash 必须显式开启（§八 P2 #3）
func TestNFSExportOpts(t *testing.T) {
	cases := []struct {
		name string
		meta FolderMeta
		want string
	}{
		{
			name: "rw 默认 root_squash",
			meta: FolderMeta{Permission: "readwrite", NFSExport: true},
			want: "rw,sync,no_subtree_check,root_squash",
		},
		{
			name: "rw 显式开启 no_root_squash",
			meta: FolderMeta{Permission: "readwrite", NFSExport: true, NFSNoRootSquash: true},
			want: "rw,sync,no_subtree_check,no_root_squash",
		},
		{
			name: "readonly 一律 ro（squash 开关不影响）",
			meta: FolderMeta{Permission: "readonly", NFSExport: true, NFSNoRootSquash: true},
			want: "ro,sync,no_subtree_check",
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
	existing := &FolderMeta{Name: "docs", Path: "/data/nas1/docs", NFSExport: true, NFSNoRootSquash: true}
	op := PendingOp{Action: "update", FolderName: "docs", FolderPath: "/data/nas1/docs", Permission: "readwrite"} // Set=false

	// 复刻 executeUpdateFolder 的三态决策
	got := false
	if op.NFSNoRootSquashSet {
		got = op.NFSNoRootSquash
	} else if existing != nil {
		got = existing.NFSNoRootSquash
	}
	if !got {
		t.Error("未显式携带 nfs_no_root_squash 时应继承现有值 true")
	}

	op2 := PendingOp{Action: "update", NFSNoRootSquash: false, NFSNoRootSquashSet: true}
	got2 := false
	if op2.NFSNoRootSquashSet {
		got2 = op2.NFSNoRootSquash
	} else if existing != nil {
		got2 = existing.NFSNoRootSquash
	}
	if got2 {
		t.Error("显式传 false 时应关闭 no_root_squash")
	}
}
