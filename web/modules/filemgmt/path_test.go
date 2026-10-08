package filemgmt

import (
	"os"
	"path/filepath"
	"testing"
)

func setupFakePool(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "public", "photos"), 0755)
	os.MkdirAll(filepath.Join(root, "fm"), 0700)
	os.Symlink("/etc", filepath.Join(root, "public", "evil")) // 指向池外
	poolRootOverride = root
	t.Cleanup(func() { poolRootOverride = "" })
	return root
}

func TestResolvePoolPathAcceptsLegal(t *testing.T) {
	root := setupFakePool(t)
	for _, rel := range []string{"", "public", "public/photos", "fm", "./public"} {
		got, err := resolvePoolPath(rel)
		if err != nil {
			t.Fatalf("resolvePoolPath(%q) 应合法, got err=%v", rel, err)
		}
		want := filepath.Clean(filepath.Join(root, rel))
		if got != want {
			t.Fatalf("resolvePoolPath(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestResolvePoolPathRejectsTraversal(t *testing.T) {
	setupFakePool(t)
	bad := []string{
		"..", "../..", "public/../../etc", "/etc/passwd", "/data/nas1/public",
		"public/../../../root", "..%2fetc", "public/./../../x",
	}
	for _, rel := range bad {
		if got, err := resolvePoolPath(rel); err == nil {
			t.Fatalf("resolvePoolPath(%q) 应被拒, got %q", rel, got)
		}
	}
}

func TestResolvePoolPathRejectsSymlinkEscape(t *testing.T) {
	setupFakePool(t)
	if got, err := resolvePoolPath("public/evil/passwd"); err == nil {
		t.Fatalf("符号链接逃逸应被拒, got %q", got)
	}
	if got, err := resolvePoolPath("public/evil"); err == nil {
		t.Fatalf("指向池外的符号链接本身应被拒, got %q", got)
	}
}

func TestResolvePoolPathKeepsInnerSymlink(t *testing.T) {
	root := setupFakePool(t)
	os.Symlink(filepath.Join(root, "fm"), filepath.Join(root, "public", "inner"))
	if _, err := resolvePoolPath("public/inner"); err != nil {
		t.Fatalf("池内符号链接应放行: %v", err)
	}
}
