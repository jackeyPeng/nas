// Package common — versions.go
//
// 第三方组件版本的单一事实源（single source of truth）。
// scripts/setup.sh 与 scripts/upload-filebrowser.sh 中的同名变量必须与此保持一致
//（shell 无法 import Go 常量；改版本时三处一起改，见 docs/plans/2026-09-19-upgrade-simplification.md）。
package common

// FileBrowserVersion 是面板下发/安装 FileBrowser 时使用的固定版本。
const FileBrowserVersion = "v2.63.17"
