package diskmgmt

import "testing"

func TestFstabOptions(t *testing.T) {
	cases := []struct {
		fstype string
		want   string
	}{
		{"xfs", "defaults,prjquota"},
		{"ext4", "defaults"},
		{"ext3", "defaults"},
		{"btrfs", "defaults"},
		{"", "defaults"},
	}
	for _, c := range cases {
		if got := fstabOptions(c.fstype); got != c.want {
			t.Errorf("fstabOptions(%q) = %q, want %q", c.fstype, got, c.want)
		}
	}
}
