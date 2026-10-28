package diskmgmt

import (
	"reflect"
	"testing"
)

// Fix 2：选盘参数（disks）与空闲盘集合求交集。交集为空时调用方必须报错中止，
// 不能静默回退到全部空闲盘（那会把操作范围从"用户选中的盘"扩大成"全部空闲盘"）。
func TestFilterSelectedDisks(t *testing.T) {
	unused := []string{"/dev/sdb", "/dev/sdc", "/dev/sdd"}

	t.Run("正常交集", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "/dev/sdb,/dev/sdc")
		if !hasSel {
			t.Fatalf("hasSel = false, want true")
		}
		want := []string{"/dev/sdb", "/dev/sdc"}
		if !reflect.DeepEqual(filtered, want) {
			t.Errorf("filtered = %v, want %v", filtered, want)
		}
	})

	t.Run("交集为空_选盘已不在空闲盘里", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "/dev/sdz")
		if !hasSel {
			t.Fatalf("hasSel = false, want true")
		}
		if len(filtered) != 0 {
			t.Errorf("filtered = %v, want empty", filtered)
		}
	})

	t.Run("sel为空串_未传参数_行为不变", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "")
		if hasSel {
			t.Fatalf("hasSel = true, want false")
		}
		if filtered != nil {
			t.Errorf("filtered = %v, want nil", filtered)
		}
	})

	t.Run("sel含空白项", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, " /dev/sdb , , /dev/sdd ")
		if !hasSel {
			t.Fatalf("hasSel = false, want true")
		}
		want := []string{"/dev/sdb", "/dev/sdd"}
		if !reflect.DeepEqual(filtered, want) {
			t.Errorf("filtered = %v, want %v", filtered, want)
		}
	})

	t.Run("sel全是空白_视为未传", func(t *testing.T) {
		filtered, hasSel := filterSelectedDisks(unused, "   ")
		if hasSel {
			t.Fatalf("hasSel = true, want false")
		}
		if filtered != nil {
			t.Errorf("filtered = %v, want nil", filtered)
		}
	})
}
