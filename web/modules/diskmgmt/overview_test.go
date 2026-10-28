package diskmgmt

import (
	"sync"
	"testing"
)

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

// TestOverviewCachesConcurrentAccess 验证 consensus P1-B（C1）的修法：
// cachedPVMappings/cachedRAIDMembers 两个包级变量在并发场景下读写不再 data race。
// 多 goroutine 并发调用 getCachedPVMappings/getCachedRAIDMappings 以及
// resetOverviewCaches（模拟 buildStorageOverview 的并发构建），go test -race
// 必须干净。断言上只验证不 panic/不 race（getter 返回值在并发 reset 下允许
// 看到任意一次填充结果，这是预期的"构建被串行化"行为，不是本测试要验证的点）。
func TestOverviewCachesConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 3)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = getCachedPVMappings()
		}()
		go func() {
			defer wg.Done()
			_ = getCachedRAIDMembers()
		}()
		go func() {
			defer wg.Done()
			resetOverviewCaches()
		}()
	}
	wg.Wait()
}

// TestBuildStorageOverviewConcurrentNoRace 并发调用 buildStorageOverview 本身
// （真实场景：多个请求同时打存储总览页），验证不 data race。测试环境没有真实
// pvs/mdadm，返回内容为空集是预期的，这里只关心并发安全性。
func TestBuildStorageOverviewConcurrentNoRace(t *testing.T) {
	var wg sync.WaitGroup
	const n = 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = buildStorageOverview()
		}()
	}
	wg.Wait()
}
