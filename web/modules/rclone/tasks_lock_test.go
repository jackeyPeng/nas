package rclone

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// withTempTasksFile 把包级 dataDir/tasksFile 临时指向 t.TempDir()，
// 测试结束后还原，避免污染真实 /opt/nas/data/rclone。
func withTempTasksFile(t *testing.T) {
	t.Helper()
	oldDataDir, oldTasksFile := dataDir, tasksFile
	dataDir = t.TempDir()
	tasksFile = dataDir + "/tasks.json"
	t.Cleanup(func() {
		dataDir, tasksFile = oldDataDir, oldTasksFile
	})
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir temp dataDir: %v", err)
	}
}

// TestTasksMuNoLostUpdate 并发跑「后台 setTaskRunState」与「用户端 toggle」
// 两类读-改-写事务，验证 tasksMu 把每次 load→改→save 整体串行化后不会
// 发生 lost update：N 个任务各被 toggle 与 setTaskRunState 各打一次标记，
// 结束后用一个全局计数器核对「总共成功落盘的事务次数」应等于触发次数，
// 不会因为交叉覆盖而丢失。
func TestTasksMuNoLostUpdate(t *testing.T) {
	withTempTasksFile(t)

	const nTasks = 5
	const nRoundsPerTask = 30

	// 建 nTasks 个任务
	tasksMu.Lock()
	var seed []SyncTask
	for i := 0; i < nTasks; i++ {
		seed = append(seed, SyncTask{ID: fmt.Sprintf("t%d", i), Name: fmt.Sprintf("task-%d", i), Enabled: true})
	}
	if err := saveTasksLocked(seed); err != nil {
		tasksMu.Unlock()
		t.Fatalf("saveTasksLocked seed: %v", err)
	}
	tasksMu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < nTasks; i++ {
		id := fmt.Sprintf("t%d", i)
		// 并发 A：模拟后台 setTaskRunState 反复写运行状态
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for r := 0; r < nRoundsPerTask; r++ {
				setTaskRunState(id, "success", "2026-01-01 00:00:00", fmt.Sprintf("run-%d", r))
			}
		}(id)
		// 并发 B：模拟用户端 toggle 反复翻转 enabled
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for r := 0; r < nRoundsPerTask; r++ {
				tasksMu.Lock()
				tasks := loadTasksLocked()
				for i := range tasks {
					if tasks[i].ID == id {
						tasks[i].Enabled = !tasks[i].Enabled
						saveTasksLocked(tasks)
						break
					}
				}
				tasksMu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	// 核对：任务数不变（没有因为交叉写丢任务、也没有重复），
	// 且每个任务的 LastMessage 是合法的 "run-N" 格式（没有被半写的 JSON 污染）。
	tasksMu.Lock()
	final := loadTasksLocked()
	tasksMu.Unlock()

	if len(final) != nTasks {
		t.Fatalf("最终任务数 = %d, want %d（lost update 可能把任务列表写丢/写乱）", len(final), nTasks)
	}
	seen := map[string]bool{}
	for _, task := range final {
		if seen[task.ID] {
			t.Errorf("任务 %s 重复出现，说明曾有并发写入互相覆盖成不一致状态", task.ID)
		}
		seen[task.ID] = true
		var runNum int
		if _, err := fmt.Sscanf(task.LastMessage, "run-%d", &runNum); err != nil {
			t.Errorf("任务 %s LastMessage = %q 不是预期格式，可能是并发写入损坏了字段", task.ID, task.LastMessage)
		}
	}
}
