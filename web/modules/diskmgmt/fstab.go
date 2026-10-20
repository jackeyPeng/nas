package diskmgmt

// fstabOptions 根据文件系统类型返回 fstab 挂载选项。
// xfs 加 prjquota 以支持面板的目录级配额功能（quota.go/users/quota.go 均依赖此挂载选项）；
// 其它文件系统维持 defaults，不引入未经验证的挂载参数。
func fstabOptions(fstype string) string {
	if fstype == "xfs" {
		return "defaults,prjquota"
	}
	return "defaults"
}
