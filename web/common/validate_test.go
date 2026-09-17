package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBlockDevicePattern(t *testing.T) {
	// 白名单命中（存在性在单测环境不保证，这里只测 pattern 部分逻辑，
	// 用 /dev/null 之类不命中块设备类型的会被 Stat 检查拦下——所以纯 pattern
	// 测试通过临时构造不存在的合法路径，预期错误是"设备不存在"而不是"非法设备路径"）
	legal := []string{
		"/dev/sda", "/dev/sdb1", "/dev/sdz", "/dev/sdaa2",
		"/dev/nvme0n1", "/dev/nvme1n2p3",
		"/dev/vda", "/dev/vdb1",
		"/dev/md0", "/dev/md127",
		"/dev/dm-0", "/dev/mapper/vg_nas-data",
		"/dev/vg_nas/data",
		"/dev/disk/by-id/ata-WDC_WD40EFRX-68N32N0_WD-WCC4E1234567",
	}
	for _, d := range legal {
		err := ValidateBlockDevice(d)
		if err != nil && !os.IsNotExist(err) {
			// 允许"设备不存在/不是块设备"（本机没有这些设备），不允许"非法设备路径"
			if contains(err.Error(), "非法设备路径") {
				t.Errorf("合法设备 %s 被白名单拒绝: %v", d, err)
			}
		}
	}

	illegal := []string{
		"",
		"sda",                   // 无 /dev/ 前缀
		"/etc/passwd",           // 普通文件
		"/dev/../etc/passwd",    // 穿越
		"/dev/; rm -rf /",       // 注入
		"/dev/$(whoami)",        // 注入
		"/dev/foo bar",          // 空格
		"/dev/sda rm -rf /",     // 参数注入
		"//dev/sda",             // 双斜杠
		"/dev/",                 // 空设备名
		"/dev/mapper/../../etc", // mapper 穿越
	}
	for _, d := range illegal {
		err := ValidateBlockDevice(d)
		if err == nil {
			t.Errorf("非法设备 %q 未被拒绝", d)
		}
	}
}

func TestValidateBlockDeviceRejectsNonBlock(t *testing.T) {
	// /dev/null 是字符设备，必须拒绝
	if err := ValidateBlockDevice("/dev/null"); err == nil {
		t.Error("/dev/null 是字符设备，应被拒绝")
	}
	// 普通文件必须拒绝（pattern 本身就会拦下 /tmp，这里再验证存在但非设备的情况）
	tmp := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(tmp, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// tmp 路径不匹配 /dev/ pattern，也应拒绝
	if err := ValidateBlockDevice(tmp); err == nil {
		t.Error("普通文件路径应被拒绝")
	}
}

func TestValidateBlockDeviceList(t *testing.T) {
	if _, err := ValidateBlockDeviceList(""); err == nil {
		t.Error("空列表应报错")
	}
	if _, err := ValidateBlockDeviceList("/dev/sda,/etc/passwd"); err == nil {
		t.Error("列表中混入非法路径应报错")
	}
	// 全合法但可能不存在：错误不应是"非法设备路径"
	_, err := ValidateBlockDeviceList("/dev/sdb, /dev/sdc")
	if err != nil && contains(err.Error(), "非法设备路径") {
		t.Errorf("合法列表被白名单拒绝: %v", err)
	}
}

func TestValidateDataPath(t *testing.T) {
	ok := []struct{ in, want string }{
		{"/data/photos", "/data/photos"},
		{"/data/photos/", "/data/photos"},
		{"/data/a//b", "/data/a/b"},
		{"/data/nas1/jacky", "/data/nas1/jacky"},
	}
	for _, c := range ok {
		got, err := ValidateDataPath(c.in, false)
		if err != nil {
			t.Errorf("ValidateDataPath(%q) 意外报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ValidateDataPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	bad := []string{
		"/data/../etc/passwd", // 穿越（核心用例）
		"/data/../../root",    // 双穿越
		"/etc/passwd",         // 完全在外面
		"data/photos",         // 相对路径
		"/datax/photos",       // 前缀相似但不同目录
		"/data",               // 根目录（allowDataRoot=false）
		"",
		"   ",
	}
	for _, p := range bad {
		if got, err := ValidateDataPath(p, false); err == nil {
			t.Errorf("ValidateDataPath(%q) 应被拒绝, got %q", p, got)
		}
	}

	// allowDataRoot=true 时 /data 本身合法
	if _, err := ValidateDataPath("/data", true); err != nil {
		t.Errorf("allowDataRoot 时 /data 应通过: %v", err)
	}
	// 软链形式的穿越也要拦（/data/link -> /etc 由调用侧 realpath 处理，这里保证字面 ".." 拦截）
	if _, err := ValidateDataPath("/data/photos/..", false); err == nil {
		t.Error("/data/photos/.. Clean 后是 /data，allowDataRoot=false 应拒绝")
	}
}

func TestValidateMountPoint(t *testing.T) {
	ok := []string{"/data", "/data/nas1", "/mnt/backup", "/mnt/usb1"}
	for _, p := range ok {
		if _, err := ValidateMountPoint(p); err != nil {
			t.Errorf("ValidateMountPoint(%q) 意外报错: %v", p, err)
		}
	}
	bad := []string{
		"/", "/etc", "/usr", "/var/lib", "/home/jacky", "/boot/efi",
		"/bin/../etc", "relative", "",
	}
	for _, p := range bad {
		if got, err := ValidateMountPoint(p); err == nil {
			t.Errorf("ValidateMountPoint(%q) 应被拒绝, got %q", p, got)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
