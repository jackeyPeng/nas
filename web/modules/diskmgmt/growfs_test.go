package diskmgmt

import "testing"

func TestParseMountPointOutput(t *testing.T) {
	mp, err := parseMountPointOutput("/dev/vg_nas/data", "/data/nas1\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mp != "/data/nas1" {
		t.Errorf("mp = %q, want /data/nas1", mp)
	}
}

func TestParseMountPointOutputTrimsWhitespace(t *testing.T) {
	mp, err := parseMountPointOutput("/dev/sdb1", "   /data/nas2   \n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mp != "/data/nas2" {
		t.Errorf("mp = %q, want /data/nas2", mp)
	}
}

func TestParseMountPointOutputUnmounted(t *testing.T) {
	_, err := parseMountPointOutput("/dev/sdb1", "")
	if err == nil {
		t.Fatal("expected error for unmounted device, got nil")
	}
}

func TestParseMountPointOutputUnmountedWhitespaceOnly(t *testing.T) {
	_, err := parseMountPointOutput("/dev/sdb1", "   \n")
	if err == nil {
		t.Fatal("expected error for whitespace-only output, got nil")
	}
}
