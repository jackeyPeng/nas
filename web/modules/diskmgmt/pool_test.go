package diskmgmt

import "testing"

// Fix 5b：删 LVM/RAID/single 池时，fstab 清理只删属于被删池的行，separate 模式
// 多池机器删一个池不应抹掉其他保留池的开机挂载。
func TestFilterFstabForDeletedPool(t *testing.T) {
	t.Run("多nas挂载点只删目标", func(t *testing.T) {
		fstab := "UUID=aaa /data/nas1 xfs defaults,prjquota 0 0\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, "")
		want := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("LVM_mapper行按vgName删除", func(t *testing.T) {
		fstab := "/dev/mapper/vg_nas-data /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/mapper/vg_other-data /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, "vg_nas")
		want := "/dev/mapper/vg_other-data /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("LVM_dev_vgname路径形式按前缀删除", func(t *testing.T) {
		fstab := "/dev/vg_nas/data /data/nas1 xfs defaults,prjquota 0 0\n" +
			"/dev/vg_other/data /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, nil, "vg_nas")
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
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, "")
		want := "/dev/sdc1 /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("注释和空行原样保留", func(t *testing.T) {
		fstab := "# fstab comment\n\nUUID=aaa /data/nas1 xfs defaults,prjquota 0 0\n" +
			"UUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, "")
		want := "# fstab comment\n\nUUID=bbb /data/nas2 xfs defaults,prjquota 0 0\n"
		if got != want {
			t.Errorf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("无匹配时输出与输入一致", func(t *testing.T) {
		fstab := "UUID=bbb /data/nas2 xfs defaults,prjquota 0 0"
		got := filterFstabForDeletedPool(fstab, []string{"/data/nas1"}, "")
		if got != fstab {
			t.Errorf("got:\n%q\nwant unchanged:\n%q", got, fstab)
		}
	})
}
