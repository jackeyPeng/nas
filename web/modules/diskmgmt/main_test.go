package diskmgmt

import (
	"os"
	"testing"
)

// TestMain 为整个 diskmgmt 测试二进制固定 NAS_DATA_DIR 到一个临时目录。
//
// initConfigDB() 用 configDBOnce sync.Once 把 folders.db 的路径永久锁定在
// "第一次被调用时 NAS_DATA_DIR 的值"（见 config_sync.go configDBPath 注释），
// 一旦某个测试在它之前调用了任何 DB 相关函数（GetAllFolderMeta/SyncFolderMeta/
// ApplyPendingOps 等），后续所有测试用例都会复用这个连接——如果不预先固定，
// 默认路径是 "/opt/nas/data/folders.db"，测试环境下这个目录很可能不存在或
// 没权限创建，在开发机上更不应该被测试写入真实路径。
//
// 这里用 TestMain 保证 NAS_DATA_DIR 在任何测试代码跑之前就指向一次性临时目录，
// 本包目前没有任何既有测试会触碰真实 DB（已核实：除本文件新增的用例外，
// 全部测试走的是 generateSambaConfigWithMetas/generateNFSConfigWithMetas
// 等"可测核心"绕开真实连接，见 config_sync_errors_test.go 顶部注释），
// 所以这个固定不改变任何既有测试的行为，只让新增的 DB 相关用例变得安全可跑。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nas-diskmgmt-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	os.Setenv("NAS_DATA_DIR", dir)
	os.Exit(m.Run())
}
