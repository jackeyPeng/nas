package diskmgmt

import "testing"

func TestPartitionDevice(t *testing.T) {
	cases := []struct {
		dev  string
		n    int
		want string
	}{
		{"/dev/sdb", 1, "/dev/sdb1"},
		{"/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"/dev/loop0", 1, "/dev/loop0p1"},
		{"/dev/vda", 2, "/dev/vda2"},
		{"", 1, ""},
	}
	for _, c := range cases {
		if got := partitionDevice(c.dev, c.n); got != c.want {
			t.Errorf("partitionDevice(%q, %d) = %q, want %q", c.dev, c.n, got, c.want)
		}
	}
}
