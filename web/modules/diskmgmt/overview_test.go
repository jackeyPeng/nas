package diskmgmt

import "testing"

// TestMatchVGForMapper 验证 A4：/dev/mapper/<name> 反推 VG 名时，优先做精确前缀
// 匹配（避免短名 VG 如 "vg" 在列表顺序靠前时，对 "vg_nas-data" 这类长名 mapper
// 产生误命中的 Contains 兜底匹配）。
func TestMatchVGForMapper(t *testing.T) {
	cases := []struct {
		name       string
		mapperName string
		vgList     []string
		want       string
	}{
		{
			name:       "普通mapper名直接前缀命中",
			mapperName: "vg_nas-data",
			vgList:     []string{"vg_nas"},
			want:       "vg_nas",
		},
		{
			name:       "VG名含连字符需转义为--再匹配",
			mapperName: "vg--nas-data",
			vgList:     []string{"vg-nas"},
			want:       "vg-nas",
		},
		{
			name:       "短名vg排在长名vg_nas前_长名仍应精确前缀命中不被短名Contains误命中",
			mapperName: "vg_nas-data",
			vgList:     []string{"vg", "vg_nas"},
			want:       "vg_nas",
		},
		{
			name:       "长名vg_nas排在短名vg前_同样应该是长名精确命中",
			mapperName: "vg_nas-data",
			vgList:     []string{"vg_nas", "vg"},
			want:       "vg_nas",
		},
		{
			name:       "无任何VG匹配返回空串",
			mapperName: "vg_other-data",
			vgList:     []string{"vg_nas", "vg_backup"},
			want:       "",
		},
		{
			name:       "空VG列表返回空串",
			mapperName: "vg_nas-data",
			vgList:     nil,
			want:       "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchVGForMapper(c.mapperName, c.vgList)
			if got != c.want {
				t.Errorf("matchVGForMapper(%q, %v) = %q, want %q", c.mapperName, c.vgList, got, c.want)
			}
		})
	}
}
