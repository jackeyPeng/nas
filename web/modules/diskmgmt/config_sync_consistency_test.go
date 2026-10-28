package diskmgmt

import "testing"

// TestExtractManagedSection 验证从配置文件内容中提取 managedStart…managedEnd
// 区间（含标记行）的四种场景：一致段存在、无托管段、孤立标记、多段残留取
// 第一个 START 到最后一个 END（consensus P1-B/C2）。
func TestExtractManagedSection(t *testing.T) {
	t.Run("正常托管段", func(t *testing.T) {
		content := "# before\n" + managedStart + "\n[a]\n" + managedEnd + "\n# after\n"
		got, ok := extractManagedSection(content)
		if !ok {
			t.Fatal("应该找到托管段")
		}
		want := managedStart + "\n[a]\n" + managedEnd
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("无托管段", func(t *testing.T) {
		content := "# just some config\nkey = value\n"
		_, ok := extractManagedSection(content)
		if ok {
			t.Error("不应找到托管段")
		}
	})

	t.Run("只有孤立START无END", func(t *testing.T) {
		content := "# before\n" + managedStart + "\n[a]\n"
		_, ok := extractManagedSection(content)
		if ok {
			t.Error("孤立 START 不构成完整托管段")
		}
	})

	t.Run("只有孤立END无START", func(t *testing.T) {
		content := "# before\n[a]\n" + managedEnd + "\n"
		_, ok := extractManagedSection(content)
		if ok {
			t.Error("孤立 END 不构成完整托管段")
		}
	})

	t.Run("多段残留取第一个START到最后一个END", func(t *testing.T) {
		content := managedStart + "\n[old]\n" + managedEnd + "\n# stray text\n" + managedStart + "\n[new]\n" + managedEnd + "\n"
		got, ok := extractManagedSection(content)
		if !ok {
			t.Fatal("应该找到托管段")
		}
		want := managedStart + "\n[old]\n" + managedEnd + "\n# stray text\n" + managedStart + "\n[new]\n" + managedEnd
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestNormalizeManagedBlock 验证行尾空白与末尾换行归一化：
// 这两类差异不应导致误报配置漂移。
func TestNormalizeManagedBlock(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"行尾空格被裁剪", "[a]   \nkey = value\t\n", "[a]\nkey = value\n"},
		{"缺末尾换行补上", "[a]\nkey = value", "[a]\nkey = value\n"},
		{"多余末尾换行收敛为一个", "[a]\nkey = value\n\n\n", "[a]\nkey = value\n"},
		{"已经规范的内容保持不变", "[a]\nkey = value\n", "[a]\nkey = value\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeManagedBlock(c.in)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestManagedSectionDrifted_FourScenarios 覆盖任务要求的四种 fixture：
// 一致、漂移、无托管段（走 extractManagedSection 的 ok=false，不应调用本函数）、
// 多段残留（取第一个 START 到最后一个 END 后内容仍可比较）。
func TestManagedSectionDrifted_FourScenarios(t *testing.T) {
	metas := []FolderMeta{
		{Name: "public", Path: "/data/nas1/public", Permission: "readwrite", SambaShare: true, NFSExport: true},
	}
	expectedSamba := generateManagedBlock(metas, "samba")
	expectedNFS := generateManagedBlock(metas, "nfs")

	t.Run("一致_不应报漂移", func(t *testing.T) {
		if managedSectionDrifted(expectedSamba, expectedSamba) {
			t.Error("内容完全一致不应判定为漂移")
		}
		if managedSectionDrifted(expectedNFS, expectedNFS) {
			t.Error("内容完全一致不应判定为漂移")
		}
	})

	t.Run("漂移_实际内容少了一个共享", func(t *testing.T) {
		emptyMetas := []FolderMeta{}
		staleActual := generateManagedBlock(emptyMetas, "samba")
		if !managedSectionDrifted(staleActual, expectedSamba) {
			t.Error("实际内容与期望不同（少了一个共享）应判定为漂移")
		}
	})

	t.Run("漂移_字段值被手工改过", func(t *testing.T) {
		tampered := managedStart + "\n[public]\n   path = /data/nas1/HACKED\n" + managedEnd + "\n"
		if !managedSectionDrifted(tampered, expectedSamba) {
			t.Error("手工改过 path 字段应判定为漂移")
		}
	})

	t.Run("无托管段_由extractManagedSection的ok标志处理不进入本函数", func(t *testing.T) {
		content := "# no managed block here\n"
		_, ok := extractManagedSection(content)
		if ok {
			t.Fatal("不应找到托管段，调用方不应再调 managedSectionDrifted 做内容比对")
		}
	})

	t.Run("多段残留_提取出的混合内容与单一期望输出比较判定为漂移", func(t *testing.T) {
		// 多段残留场景下 extractManagedSection 会把两段整体都提取出来，
		// 这段混合内容显然不等于只含一份正常输出的 expectedSamba，必然判定漂移——
		// 这正是期望行为：多段残留本身就是一种需要被发现的配置问题。
		content := managedStart + "\n[old]\n" + managedEnd + "\n# stray\n" + managedStart + "\n[public]\n" + managedEnd + "\n"
		actual, ok := extractManagedSection(content)
		if !ok {
			t.Fatal("应该找到托管段（即便是残留的多段）")
		}
		if !managedSectionDrifted(actual, expectedSamba) {
			t.Error("多段残留内容与正常期望输出不同，应判定为漂移")
		}
	})
}

// TestGenerateManagedBlock_MatchesRealBuilders 验证 generateManagedBlock（期望
// 输出）与 buildSambaManagedBlock/buildNFSManagedBlock（GenerateSambaConfig/
// GenerateNFSConfig 真正写盘用的那一份）逐字节一致——这是本次修法的核心：
// 此前 generateManagedBlock 是"简化版预览"，和真实写盘内容对不上，没法做
// 有意义的比对。现在两者必须由同一份函数产生。
func TestGenerateManagedBlock_MatchesRealBuilders(t *testing.T) {
	metas := []FolderMeta{
		{Name: "public", Path: "/data/nas1/public", Permission: "readwrite", SambaShare: true, NFSExport: true, RecycleBin: true},
		{Name: "alice", Path: "/data/nas1/alice", Permission: "readwrite", ValidUsers: "alice", SambaShare: true},
	}

	gotSamba := generateManagedBlock(metas, "samba")
	wantSamba := buildSambaManagedBlock(metas, getNASUser())
	if gotSamba != wantSamba {
		t.Errorf("samba 托管段期望输出与 buildSambaManagedBlock 不一致\ngot:\n%s\nwant:\n%s", gotSamba, wantSamba)
	}

	gotNFS := generateManagedBlock(metas, "nfs")
	wantNFS := buildNFSManagedBlock(metas)
	if gotNFS != wantNFS {
		t.Errorf("nfs 托管段期望输出与 buildNFSManagedBlock 不一致\ngot:\n%s\nwant:\n%s", gotNFS, wantNFS)
	}
}

// TestGenerateManagedBlock_UnknownConfigType 验证未知 configType 返回空串而非 panic。
func TestGenerateManagedBlock_UnknownConfigType(t *testing.T) {
	if got := generateManagedBlock(nil, "unknown"); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}
