package diskmgmt

import "testing"

// Fix 1a：整盘无 FSTYPE 但子分区带文件系统签名（含未挂载）时，父盘不应停留在
// "unused" —— 否则这块盘会被 overview.go 的 FreeDisks 收录，扩容/向导随后对其
// wipefs，造成数据丢失。
func TestClassifyChildPartition(t *testing.T) {
	cases := []struct {
		name           string
		mountpoint     string
		fstype         string
		parentType     string
		wantChildType  string
		wantParentType string
	}{
		{
			name:       "整盘无fs+子分区xfs未挂载_父盘应为data",
			mountpoint: "", fstype: "xfs", parentType: "unused",
			wantChildType: "data", wantParentType: "data",
		},
		{
			name:       "整盘无fs+子分区ext4未挂载_父盘应为data",
			mountpoint: "", fstype: "ext4", parentType: "unused",
			wantChildType: "data", wantParentType: "data",
		},
		{
			name:       "整盘无fs+子分区swap_父盘应为data",
			mountpoint: "", fstype: "swap", parentType: "unused",
			wantChildType: "swap", wantParentType: "data",
		},
		{
			name:       "子分区挂载根_父盘保持system不被覆盖",
			mountpoint: "/", fstype: "ext4", parentType: "unused",
			wantChildType: "system", wantParentType: "system",
		},
		{
			name:       "系统盘上未挂载分区不应把system降级",
			mountpoint: "", fstype: "xfs", parentType: "system",
			wantChildType: "data", wantParentType: "system",
		},
		{
			name:       "系统盘上swap分区不应把system降级",
			mountpoint: "", fstype: "swap", parentType: "system",
			wantChildType: "swap", wantParentType: "system",
		},
		{
			name:       "子分区挂载非系统路径_父盘变data",
			mountpoint: "/data/nas1", fstype: "xfs", parentType: "unused",
			wantChildType: "data", wantParentType: "data",
		},
		{
			name:       "子分区既无挂载点也无fstype_子unused_父不变",
			mountpoint: "", fstype: "", parentType: "unused",
			wantChildType: "unused", wantParentType: "unused",
		},
		{
			name:       "子分区挂boot_父盘变system",
			mountpoint: "/boot", fstype: "ext4", parentType: "unused",
			wantChildType: "system", wantParentType: "system",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotChild, gotParent := classifyChildPartition(c.mountpoint, c.fstype, c.parentType)
			if gotChild != c.wantChildType || gotParent != c.wantParentType {
				t.Errorf("classifyChildPartition(%q, %q, %q) = (%q, %q), want (%q, %q)",
					c.mountpoint, c.fstype, c.parentType, gotChild, gotParent, c.wantChildType, c.wantParentType)
			}
		})
	}
}

// Fix 1b：hasFstypeSignature 解析 lsblk -n -r -o NAME,FSTYPE 输出，判断设备自身
// 或任一子分区是否带非空 FSTYPE（含 swap）。
func TestHasFstypeSignature(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "有xfs分区",
			out:  "sdb\nsdb1 xfs",
			want: true,
		},
		{
			name: "全空",
			out:  "sdb\nsdb1",
			want: false,
		},
		{
			name: "空输出",
			out:  "",
			want: false,
		},
		{
			name: "只有空白行",
			out:  "\n\n",
			want: false,
		},
		{
			name: "设备自身带fstype",
			out:  "sdb xfs",
			want: true,
		},
		{
			name: "swap签名",
			out:  "sdb\nsdb1 swap",
			want: true,
		},
		{
			name: "多子分区其中一个有fs",
			out:  "nvme0n1\nnvme0n1p1\nnvme0n1p2 ext4",
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasFstypeSignature(c.out); got != c.want {
				t.Errorf("hasFstypeSignature(%q) = %v, want %v", c.out, got, c.want)
			}
		})
	}
}
