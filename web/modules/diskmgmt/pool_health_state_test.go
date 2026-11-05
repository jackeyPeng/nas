package diskmgmt

import (
	"testing"
)

// TestPoolHealthState 验证 poolHealthState 对各类 RAID Health/Activity 组合及
// 非 RAID 池兜底路径推导出的三态健康徽章状态（healthy/rebuilding/degraded/
// failed）与 SyncPercent 是否符合任务书判定语义。
func TestPoolHealthState(t *testing.T) {
	cases := []struct {
		name            string
		isRaidPool      bool
		ra              *RAIDStatus
		healthy         bool
		wantState       string
		wantSyncPercent string
	}{
		{
			name:       "normal+idle→healthy",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:   "normal",
				Activity: "idle",
			},
			healthy:   true,
			wantState: "healthy",
		},
		{
			name:       "degraded+有SyncPercent→rebuilding带进度",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:      "degraded",
				Activity:    "recover",
				SyncPercent: "57.3%",
			},
			healthy:         false,
			wantState:       "rebuilding",
			wantSyncPercent: "57.3%",
		},
		{
			name:       "degraded+Activity是resync但SyncPercent为空→仍rebuilding",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:   "degraded",
				Activity: "resync",
			},
			healthy:   false,
			wantState: "rebuilding",
		},
		{
			name:       "degraded无同步无resync类activity→真降级",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:   "degraded",
				Activity: "idle",
			},
			healthy:   false,
			wantState: "degraded",
		},
		{
			name:       "failed→failed",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:   "failed",
				Activity: "idle",
			},
			healthy:   false,
			wantState: "failed",
		},
		{
			name:       "normal但Activity非idle(check)无SyncPercent→rebuilding无进度",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:   "normal",
				Activity: "check",
			},
			healthy:   true,
			wantState: "rebuilding",
		},
		{
			name:       "normal但Activity非idle(reshape)带SyncPercent→rebuilding带进度",
			isRaidPool: true,
			ra: &RAIDStatus{
				Health:      "normal",
				Activity:    "reshape",
				SyncPercent: "12.0%",
			},
			healthy:         true,
			wantState:       "rebuilding",
			wantSyncPercent: "12.0%",
		},
		{
			name:       "非RAID池healthy=true→healthy兜底",
			isRaidPool: false,
			ra:         nil,
			healthy:    true,
			wantState:  "healthy",
		},
		{
			name:       "非RAID池healthy=false→degraded兜底",
			isRaidPool: false,
			ra:         nil,
			healthy:    false,
			wantState:  "degraded",
		},
		{
			name:       "RAID池但raidMap未命中(ra=nil)且healthy=true→healthy兜底",
			isRaidPool: true,
			ra:         nil,
			healthy:    true,
			wantState:  "healthy",
		},
		{
			name:       "RAID池但raidMap未命中(ra=nil)且healthy=false→degraded兜底",
			isRaidPool: true,
			ra:         nil,
			healthy:    false,
			wantState:  "degraded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotState, gotSync := poolHealthState(tc.isRaidPool, tc.ra, tc.healthy)
			if gotState != tc.wantState {
				t.Errorf("state = %q, want %q", gotState, tc.wantState)
			}
			if gotSync != tc.wantSyncPercent {
				t.Errorf("syncPercent = %q, want %q", gotSync, tc.wantSyncPercent)
			}
		})
	}
}
