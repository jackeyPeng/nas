package diskmgmt

import "testing"

// Fix 5b：删 LVM/RAID/single 池时，fstab 清理只删属于被删池的行，separate 模式
// 多池机器删一个池不应抹掉其他保留池的开机挂载。
// A3：新增判据 (c) —— 池未挂载时删除（findmnt 查不到挂载点），只有 UUID 行能
// 命中残留的 fstab 条目，见相关子用例。
func TestFilterFstabForDeletedPool(t *testing.T) {
	t.Run("多nas挂载点只删目标", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 0\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("LVM_mapper行按vgName删除", func(t *testing.T) {
		fstab := "/dev/mapper/vg_nas-data /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/mapper/vg_other-data /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "vg_nas")
		want := "/dev/mapper/vg_other-data /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("LVM_dev_vgname路径形式按前缀删除", func(t *testing.T) {
		fstab := "/dev/vg_nas/data /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/vg_other/data /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, nil, nil, "vg_nas")
		want := "/dev/vg_other/data /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("其他池行保留_RAID分支只用挂载点判据", func(t *testing.T) {
		// RAID/single 分支 lvmVgName 传空串，即便设备路径碰巧含被删 VG 的名字
		// 也不应被判据(b)误删——这里用非 LVM 设备路径验证只判挂载点。
		fstab := "/dev/md0 /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/sdc1 /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		want := "/dev/sdc1 /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("注释和空行原样保留", func(t *testing.T) {
		fstab := "# fstab comment\n\nUUID=aaa /data/nas1 xfs defaults,prjquota 0 0\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		want := "# fstab comment\n\nUUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("无匹配时输出与输入一致", func(t *testing.T) {
		fstab := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 0"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, nil, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})

	t.Run("A3_未挂载池只剩UUID行也被删", func(t *testing.T) {
		// 池未挂载时 findmnt 查不到挂载点，deletedMounts 为空；只有判据(c)
		// 的 UUID 匹配能命中这行，否则残留会在下次开机导致 emergency mode。
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, nil, []string{"aaa"}, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("A3_其他池UUID行保留", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, nil, []string{"ccc"}, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})

	t.Run("A3_UUID为空值被跳过不panic", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, nil, []string{"", "  "}, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})

	t.Run("A3_挂载点与UUID判据同时命中去重自然成立", func(t *testing.T) {
		// 挂载态删除时 deletedMounts 和 deletedUUIDs 都非空，两个判据都指向
		// 同一行，结果应与单判据一致（不会重复删或漏删）。
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 2\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, []string{"aaa"}, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 2\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})
}

// TestFirstLine 验证 firstLine 只取多行输出首行（Minor finding：findmnt 对多
// 挂载点 LV 可能输出多行，脏值会破坏挂载点判据）。
func TestFirstLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/data/nas1\n", "/data/nas1"},
		{"/data/nas1\n/data/nas1-bind\n", "/data/nas1"},
		{"", ""},
		{"   /data/nas1   \n", "/data/nas1"},
	}
	for _, c := range cases {
		if got := firstLine(c.in); got != c.want {
			t.Errorf("firstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
