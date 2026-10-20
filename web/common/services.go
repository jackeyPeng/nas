package common

// AllowedManagedServices 返回面板允许通过 systemctl 操作（start/stop/enable/disable 等）
// 的服务白名单。多个模块（system、config）都需要对用户提交的服务名做校验，
// 统一到这里维护，避免各处白名单跑偏。
func AllowedManagedServices() map[string]bool {
	return map[string]bool{
		"smbd": true, "nmbd": true, "vsftpd": true, "nfs-server": true,
		"nas-panel": true, "fail2ban": true, "cron": true, "ssh": true,
	}
}
