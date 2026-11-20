package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// tarEntry 描述一个待写入的 tar 条目（测试专用构造器）。
type tarEntry struct {
	name     string
	content  []byte
	typeflag byte
	linkname string // 仅 symlink/hardlink 用
	mode     int64
}

// buildTarGz 按给定条目列表构造一个 tar.gz 字节流，供恶意/合法包用例复用。
func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.content)),
			Typeflag: tar.TypeReg,
		}
		if e.mode != 0 {
			hdr.Mode = e.mode
		}
		if e.typeflag != 0 {
			hdr.Typeflag = e.typeflag
		}
		if e.typeflag == tar.TypeDir {
			hdr.Size = 0
		}
		if e.linkname != "" {
			hdr.Linkname = e.linkname
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 tar header %s 失败: %v", e.name, err)
		}
		if hdr.Typeflag == tar.TypeReg && len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatalf("写 tar 内容 %s 失败: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("关闭 gzip writer: %v", err)
	}
	return buf.Bytes()
}

// validPNG 是最小合法 PNG 字节（魔数 + 任意内容，解包校验只检查魔数）。
var validPNG = append(append([]byte{}, pngMagic...), []byte("fake-png-body")...)

// validAppEntries 返回一套通过全部布局校验的合法包条目（root = photos-1.0.0）。
func validAppEntries() []tarEntry {
	const root = "photos-1.0.0/"
	return []tarEntry{
		{name: root, typeflag: tar.TypeDir},
		{name: root + "manifest.json", content: []byte(validManifestJSON)},
		{name: root + "icon.png", content: validPNG},
		{name: root + "unit.tpl", content: []byte("[Unit]\nDescription=photos\n")},
		{name: root + "app/", typeflag: tar.TypeDir},
		{name: root + "app/main", content: []byte("#!/bin/sh\necho hi\n")},
		{name: root + "README.md", content: []byte("# photos\n")},
	}
}

func TestUnpackAppPackage_Valid(t *testing.T) {
	tb := buildTarGz(t, validAppEntries())
	pkg, err := UnpackAppPackage(tb)
	if err != nil {
		t.Fatalf("合法包应解包成功: %v", err)
	}
	if pkg.Manifest.ID != "photos" {
		t.Errorf("Manifest.ID = %q", pkg.Manifest.ID)
	}
	if string(pkg.Files["app/main"]) != "#!/bin/sh\necho hi\n" {
		t.Errorf("app/main 内容不匹配: %q", pkg.Files["app/main"])
	}
	if !bytes.Equal(pkg.Tarball, tb) {
		t.Error("Tarball 字段应保留原始字节")
	}
	if len(pkg.Files["unit.tpl"]) == 0 {
		t.Error("unit.tpl 内容应保留")
	}
}

func TestUnpackAppPackage_PathTraversal_DotDot(t *testing.T) {
	entries := validAppEntries()
	entries = append(entries, tarEntry{name: "photos-1.0.0/../evil", content: []byte("evil")})
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("包含 .. 的条目应被拒绝")
	}
}

func TestUnpackAppPackage_PathTraversal_AbsolutePath(t *testing.T) {
	entries := validAppEntries()
	entries = append(entries, tarEntry{name: "/etc/evil", content: []byte("evil")})
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("绝对路径条目应被拒绝")
	}
}

func TestUnpackAppPackage_PathTraversal_SymlinkEntry(t *testing.T) {
	entries := validAppEntries()
	entries = append(entries, tarEntry{
		name:     "photos-1.0.0/app/evil-link",
		typeflag: tar.TypeSymlink,
		linkname: "/etc/passwd",
	})
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("符号链接条目应被拒绝")
	}
}

func TestUnpackAppPackage_PathTraversal_HardlinkEntry(t *testing.T) {
	entries := validAppEntries()
	entries = append(entries, tarEntry{
		name:     "photos-1.0.0/app/evil-hardlink",
		typeflag: tar.TypeLink,
		linkname: "photos-1.0.0/app/main",
	})
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("硬链接条目应被拒绝")
	}
}

func TestUnpackAppPackage_MissingIcon(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasSuffix(e.name, "icon.png") {
			continue
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("缺少 icon.png 应被拒绝")
	}
}

func TestUnpackAppPackage_BadPNGMagic(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasSuffix(e.name, "icon.png") {
			e.content = []byte("not-a-real-png")
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("PNG 魔数不匹配应被拒绝")
	}
}

func TestUnpackAppPackage_MissingUnitTpl(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasSuffix(e.name, "unit.tpl") {
			continue
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("缺少 unit.tpl 应被拒绝")
	}
}

func TestUnpackAppPackage_EmptyUnitTpl(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasSuffix(e.name, "unit.tpl") {
			e.content = []byte("   \n  ")
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("空 unit.tpl 应被拒绝")
	}
}

func TestUnpackAppPackage_EmptyAppDir(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasPrefix(e.name, "photos-1.0.0/app/") {
			continue // 去掉 app/ 下的全部文件，只留目录条目本身
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("app/ 目录为空应被拒绝")
	}
}

func TestUnpackAppPackage_RootMismatchWithManifest(t *testing.T) {
	entries := validAppEntries()
	// 把全部条目的根目录改成与 manifest（photos-1.0.0）不一致的前缀
	for i, e := range entries {
		entries[i].name = strings.Replace(e.name, "photos-1.0.0/", "photos-9.9.9/", 1)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("根目录与 manifest 不一致应被拒绝")
	}
}

func TestUnpackAppPackage_MultipleRootDirs(t *testing.T) {
	entries := validAppEntries()
	entries = append(entries, tarEntry{name: "other-root/file", content: []byte("x")})
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("多个根目录前缀应被拒绝")
	}
}

func TestUnpackAppPackage_TooLarge(t *testing.T) {
	entries := validAppEntries()
	for i, e := range entries {
		if strings.HasSuffix(e.name, "app/main") {
			entries[i].content = make([]byte, maxPackageSize+1)
		}
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("超过单包上限应被拒绝")
	}
}

func TestUnpackAppPackage_EmptyInput(t *testing.T) {
	if _, err := UnpackAppPackage(nil); err == nil {
		t.Fatal("空输入应报错")
	}
}

func TestUnpackAppPackage_NotGzip(t *testing.T) {
	if _, err := UnpackAppPackage([]byte("this is not gzip data")); err == nil {
		t.Fatal("非 gzip 数据应报错")
	}
}

func TestUnpackAppPackage_MissingManifest(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasSuffix(e.name, "manifest.json") {
			continue
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("缺少 manifest.json 应被拒绝")
	}
}

func TestUnpackAppPackage_InvalidManifestInsidePackage(t *testing.T) {
	var entries []tarEntry
	for _, e := range validAppEntries() {
		if strings.HasSuffix(e.name, "manifest.json") {
			e.content = []byte(strings.Replace(validManifestJSON, `"id": "photos",`, `"id": "BAD!",`, 1))
		}
		entries = append(entries, e)
	}
	tb := buildTarGz(t, entries)
	if _, err := UnpackAppPackage(tb); err == nil {
		t.Fatal("包内 manifest 校验失败应导致整包拒绝")
	}
}
