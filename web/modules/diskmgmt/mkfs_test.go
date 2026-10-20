package diskmgmt

import (
	"reflect"
	"testing"
)

func TestMkfsArgs(t *testing.T) {
	cases := []struct {
		fstype string
		device string
		want   []string
	}{
		{"xfs", "/dev/sdb1", []string{"-f", "/dev/sdb1"}},
		{"btrfs", "/dev/sdb1", []string{"-f", "/dev/sdb1"}},
		{"ext4", "/dev/sdb1", []string{"-F", "/dev/sdb1"}},
		{"ext3", "/dev/sdb1", []string{"-F", "/dev/sdb1"}},
		{"ext2", "/dev/sdb1", []string{"-F", "/dev/sdb1"}},
		{"unknownfs", "/dev/sdb1", []string{"/dev/sdb1"}},
	}
	for _, c := range cases {
		if got := mkfsArgs(c.fstype, c.device); !reflect.DeepEqual(got, c.want) {
			t.Errorf("mkfsArgs(%q, %q) = %v, want %v", c.fstype, c.device, got, c.want)
		}
	}
}
