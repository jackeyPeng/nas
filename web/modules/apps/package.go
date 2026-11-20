package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"
)

// maxPackageSize 单包上限 256MB（§1 应用包格式；M1 约束，防止恶意/异常大包撑爆磁盘与内存）。
const maxPackageSize = 256 * 1024 * 1024

// maxEntrySize 解包时单文件读入内存的上限，与 maxPackageSize 一致：tar 内条目总和
// 不会超过压缩前的包本身，但逐条累加校验更早发现恶意构造的超大条目。
const maxEntrySize = maxPackageSize

// pngMagic 是 PNG 文件头魔数（§1 icon.png 必须为真实 PNG）。
var pngMagic = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// AppPackage 是解包后的应用包：校验通过的 manifest + 全部文件内容 + 原始 tarball。
type AppPackage struct {
	Manifest *Manifest
	Files    map[string][]byte // key 为相对根目录前缀后的路径（如 "manifest.json"、"app/main"）
	Tarball  []byte
}

// UnpackAppPackage 解析 tar.gz 应用包并执行布局校验（§1）：
//   - 路径穿越防护：拒绝绝对路径条目、".." 片段、符号链接条目
//   - 根目录必须是单个 "<id>-<version>/" 前缀，且与 manifest 内容一致
//   - 必需文件：manifest.json（通过 ParseManifest 校验）、icon.png（PNG 魔数）、
//     unit.tpl（非空文本）、app/ 目录非空
//   - 单包不得超过 256MB
func UnpackAppPackage(tarball []byte) (*AppPackage, error) {
	if len(tarball) == 0 {
		return nil, fmt.Errorf("应用包为空")
	}
	if len(tarball) > maxPackageSize {
		return nil, fmt.Errorf("应用包超过单包上限 %dMB", maxPackageSize/1024/1024)
	}

	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("不是合法的 gzip 数据: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	files := make(map[string][]byte)
	var rootPrefix string
	var appDirHasEntry bool
	totalSize := int64(0)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar 格式错误: %v", err)
		}

		name := hdr.Name

		// 路径穿越防护：绝对路径、".." 片段、符号链接/硬链接条目一律拒绝。
		if path.IsAbs(name) || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("拒绝绝对路径条目: %s", name)
		}
		if hasDotDotSegment(name) {
			return nil, fmt.Errorf("拒绝包含 .. 的条目: %s", name)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("拒绝符号链接/硬链接条目: %s", name)
		}

		cleaned := path.Clean(name)
		if cleaned == "." {
			continue // 根目录自身的 tar 条目（有的打包工具会写入）
		}

		segments := strings.SplitN(cleaned, "/", 2)
		if rootPrefix == "" {
			rootPrefix = segments[0]
		} else if segments[0] != rootPrefix {
			return nil, fmt.Errorf("根目录不一致: 期望单一前缀 %q，发现 %q", rootPrefix, segments[0])
		}

		if hdr.Typeflag == tar.TypeDir {
			// 目录条目本身不算 app/ 非空（appDirHasEntry 只在遇到真正文件时置位）。
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("拒绝非常规文件条目: %s（类型 %v）", name, hdr.Typeflag)
		}

		if len(segments) < 2 {
			// 根目录下直属的非目录文件（如 <id>-<version> 本身是文件）不合法布局，交给后续必需文件校验报错
			continue
		}
		relPath := segments[1]

		if hdr.Size < 0 || hdr.Size > maxEntrySize {
			return nil, fmt.Errorf("条目 %s 大小异常: %d", name, hdr.Size)
		}
		totalSize += hdr.Size
		if totalSize > maxPackageSize {
			return nil, fmt.Errorf("解包后总大小超过单包上限 %dMB", maxPackageSize/1024/1024)
		}

		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size+1))
		if err != nil {
			return nil, fmt.Errorf("读取条目 %s 失败: %v", name, err)
		}
		if int64(len(data)) != hdr.Size {
			return nil, fmt.Errorf("条目 %s 实际大小与头部声明不一致（可能被篡改）", name)
		}

		files[relPath] = data
		if relPath == "app" || strings.HasPrefix(relPath, "app/") {
			appDirHasEntry = true
		}
	}

	if rootPrefix == "" {
		return nil, fmt.Errorf("应用包为空或不含任何条目")
	}

	manifestData, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("缺少必需文件: manifest.json")
	}
	m, err := ParseManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("manifest.json 校验失败: %v", err)
	}

	expectedRoot := fmt.Sprintf("%s-%s", m.ID, m.Version)
	if rootPrefix != expectedRoot {
		return nil, fmt.Errorf("根目录 %q 与 manifest 不一致（期望 %q）", rootPrefix, expectedRoot)
	}

	iconData, ok := files["icon.png"]
	if !ok {
		return nil, fmt.Errorf("缺少必需文件: icon.png")
	}
	if !bytes.HasPrefix(iconData, pngMagic) {
		return nil, fmt.Errorf("icon.png 不是合法 PNG 文件（魔数不匹配）")
	}

	unitData, ok := files["unit.tpl"]
	if !ok {
		return nil, fmt.Errorf("缺少必需文件: unit.tpl")
	}
	if len(bytes.TrimSpace(unitData)) == 0 {
		return nil, fmt.Errorf("unit.tpl 不能为空")
	}

	if !appDirHasEntry {
		return nil, fmt.Errorf("缺少必需目录或为空: app/")
	}

	return &AppPackage{
		Manifest: m,
		Files:    files,
		Tarball:  tarball,
	}, nil
}

// hasDotDotSegment 检查路径是否包含 ".." 片段（按 "/" 分隔逐段比较，
// 不依赖 Clean 的归一化结果，防止 "a/../../etc/passwd" 这类穿越在 Clean 前漏检）。
func hasDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
