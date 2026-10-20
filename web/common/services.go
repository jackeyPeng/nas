package common

// AllowedManagedServices 返回面板允许通过 systemctl 操作（start/stop/enable/disable 等）
// 的服务白名单。多个模块（system、config）都需要对用户提交的服务名做校验，
// 统一到这里维护，避免各处白名单跑偏。
// rclone-webdav/rclone-s3 是面板自装服务（见 modules/services/services.go 的
// installSingleService/installAllServices，systemd unit 写到
// /etc/systemd/system/），前端"自启动管理"页会把它们列进可切换服务，
// 不放进白名单会导致合法的 enable/disable 被误判拒绝。
func AllowedManagedServices() map[string]bool {
	return map[string]bool{
		"smbd": true, "nmbd": true, "vsftpd": true, "nfs-server": true,
		"nas-panel": true, "fail2ban": true, "cron": true, "ssh": true,
		"rclone-webdav": true, "rclone-s3": true,
	}
}
