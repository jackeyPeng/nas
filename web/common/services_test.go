package common

import "testing"

func TestAllowedManagedServices(t *testing.T) {
	allowed := AllowedManagedServices()
	for _, svc := range []string{"smbd", "nmbd", "vsftpd", "nfs-server", "nas-panel", "fail2ban", "cron", "ssh"} {
		if !allowed[svc] {
			t.Errorf("服务 %s 应在白名单中", svc)
		}
	}
	for _, svc := range []string{"", "rm -rf /", "sshd", "docker"} {
		if allowed[svc] {
			t.Errorf("服务 %q 不应在白名单中", svc)
		}
	}
}
