package health

import "testing"

func svc(active string) map[string]interface{} {
	return map[string]interface{}{"active": active}
}

func TestSystemCheckFrom(t *testing.T) {
	cases := []struct {
		name     string
		services []map[string]interface{}
		wantStat string
		wantDet  string
	}{
		{
			name: "7 active + 1 not-installed",
			services: []map[string]interface{}{
				svc("active"), svc("active"), svc("active"), svc("active"),
				svc("active"), svc("active"), svc("active"), svc("not-installed"),
			},
			wantStat: "ok",
			wantDet:  "7/7 服务全部运行中",
		},
		{
			name: "全 active",
			services: []map[string]interface{}{
				svc("active"), svc("active"), svc("active"),
			},
			wantStat: "ok",
			wantDet:  "3/3 服务全部运行中",
		},
		{
			name: "有 failed",
			services: []map[string]interface{}{
				svc("active"), svc("active"), svc("failed"), svc("not-installed"),
			},
			wantStat: "error",
			wantDet:  "2/3 服务运行中，1 个异常",
		},
		{
			name: "全 not-installed",
			services: []map[string]interface{}{
				svc("not-installed"), svc("not-installed"),
			},
			wantStat: "warn",
			wantDet:  "未检测到已安装的 NAS 服务",
		},
		{
			name: "active+unknown 混合",
			services: []map[string]interface{}{
				svc("active"), svc("active"), svc("unknown"),
			},
			wantStat: "ok",
			wantDet:  "2/3 服务全部运行中",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := systemCheckFrom(tc.services)
			if got.Status != tc.wantStat {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStat)
			}
			if got.Detail != tc.wantDet {
				t.Errorf("Detail = %q, want %q", got.Detail, tc.wantDet)
			}
		})
	}
}
