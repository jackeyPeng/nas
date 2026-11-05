package diskmgmt

import (
	"testing"
)

// TestRaidAlertFromStatus 验证 raidAlertFromStatus 对各类 RAID 状态生成的告警
// level/message 是否符合注册表语义（system_registry.go classifyMdArray）：
// 降级/故障 + 有重建进度 = warning「初始重建中」而非误报 critical「数据风险」。
func TestRaidAlertFromStatus(t *testing.T) {
	cases := []struct {
		name      string
		ra        RAIDStatus
		wantNil   bool
		wantLevel string
		wantMsg   string
	}{
		{
			name: "degraded且有重建进度→warning初始重建文案",
			ra: RAIDStatus{
				Device:      "/dev/md0",
				State:       "degraded",
				SyncPercent: "92.0%",
			},
			wantLevel: "warning",
			wantMsg:   "RAID /dev/md0 初始重建/同步中 92.0%，期间冗余降低，完成后自动恢复",
		},
		{
			name: "degraded且无重建进度→critical数据风险",
			ra: RAIDStatus{
				Device: "/dev/md0",
				State:  "degraded",
			},
			wantLevel: "critical",
			wantMsg:   "RAID /dev/md0 已降级，数据处于风险中，建议尽快处理",
		},
		{
			name: "FAILED状态→critical数据风险",
			ra: RAIDStatus{
				Device: "/dev/md1",
				State:  "FAILED",
			},
			wantLevel: "critical",
			wantMsg:   "RAID /dev/md1 已降级，数据处于风险中，建议尽快处理",
		},
		{
			name: "resyncing且有进度→warning正在重建同步",
			ra: RAIDStatus{
				Device:      "/dev/md0",
				State:       "resyncing",
				SyncPercent: "45.0%",
			},
			wantLevel: "warning",
			wantMsg:   "RAID /dev/md0 正在重建/同步 45.0%",
		},
		{
			name: "其他异常state且无进度→warning状态异常",
			ra: RAIDStatus{
				Device: "/dev/md2",
				State:  "inactive",
			},
			wantLevel: "warning",
			wantMsg:   "RAID /dev/md2 状态异常: inactive",
		},
		{
			name: "active→不告警",
			ra: RAIDStatus{
				Device: "/dev/md0",
				State:  "active",
			},
			wantNil: true,
		},
		{
			name: "clean→不告警",
			ra: RAIDStatus{
				Device: "/dev/md0",
				State:  "clean",
			},
			wantNil: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := raidAlertFromStatus(tc.ra)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("want nil alert, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want alert, got nil")
			}
			if got.Level != tc.wantLevel {
				t.Errorf("level = %q, want %q", got.Level, tc.wantLevel)
			}
			if got.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", got.Message, tc.wantMsg)
			}
			if got.Device != tc.ra.Device {
				t.Errorf("device = %q, want %q", got.Device, tc.ra.Device)
			}
		})
	}
}
