package apps

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
)

func openTestAppsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openAppsDB(t.TempDir())
	if err != nil {
		t.Fatalf("openAppsDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAllocatePort_RequestedThenFallback(t *testing.T) {
	db := openTestAppsDB(t)
	exec := newFakeExecutor()

	port, err := allocatePort(db, exec, 18010)
	if err != nil || port != 18010 {
		t.Fatalf("requested available port: got %d, err %v; want 18010", port, err)
	}
	if err := reservePort(db, port); err != nil {
		t.Fatalf("reservePort: %v", err)
	}

	port, err = allocatePort(db, exec, 18010)
	if err != nil || port == 18010 {
		t.Fatalf("occupied requested port should fall back: got %d, err %v", port, err)
	}
	if port < minPort || port > maxPort {
		t.Fatalf("fallback port %d outside range", port)
	}
}

func TestAllocatePort_ProbeRejectsExternallyBusyPort(t *testing.T) {
	db := openTestAppsDB(t)
	exec := newFakeExecutor()
	exec.ports[18010] = true

	port, err := allocatePort(db, exec, 18010)
	if err != nil {
		t.Fatalf("allocatePort: %v", err)
	}
	if port == 18010 {
		t.Fatal("port with failed bind probe must not be returned")
	}
}

func TestAllocatePort_ConcurrentReservationsAreUnique(t *testing.T) {
	db := openTestAppsDB(t)
	exec := newFakeExecutor()
	var installMu sync.Mutex
	const n = 24
	ports := make(chan int, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			installMu.Lock()
			defer installMu.Unlock()
			port, err := allocatePort(db, exec, 18010)
			if err != nil {
				errs <- err
				return
			}
			if err := reservePort(db, port); err != nil {
				errs <- err
				return
			}
			ports <- port
		}()
	}
	wg.Wait()
	close(ports)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent allocation: %v", err)
	}
	seen := map[int]bool{}
	for p := range ports {
		if seen[p] {
			t.Errorf("port %d allocated more than once", p)
		}
		seen[p] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d unique ports, want %d", len(seen), n)
	}
}

func TestGenerateAppSecret_Is32BytesHexAndUnique(t *testing.T) {
	a, err := generateAppSecret()
	if err != nil {
		t.Fatalf("generateAppSecret: %v", err)
	}
	b, err := generateAppSecret()
	if err != nil {
		t.Fatalf("generateAppSecret: %v", err)
	}
	if len(a) != secretByteLen*2 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("secret must be %d lowercase hex chars, got %q", secretByteLen*2, a)
	}
	if a == b {
		t.Fatal("two generated secrets unexpectedly match")
	}
}

func TestInstalledAppRegistryRoundTripAndDelete(t *testing.T) {
	db := openTestAppsDB(t)
	app := installedApp{ID: "photos", Name: "Photos", Version: "1.2.3", Spec: "1.0", Port: 18010, Unit: appUnitName("photos"), Secret: "secret", Status: statusPending}
	if err := insertInstalledApp(db, app); err != nil {
		t.Fatalf("insertInstalledApp: %v", err)
	}
	got, err := getInstalledApp(db, app.ID)
	if err != nil || got == nil {
		t.Fatalf("getInstalledApp: got %+v, err %v", got, err)
	}
	if got.ID != app.ID || got.Version != app.Version || got.Secret != app.Secret || got.Status != statusPending {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if err := updateInstalledAppStatus(db, app.ID, statusInstalled); err != nil {
		t.Fatalf("updateInstalledAppStatus: %v", err)
	}
	got, err = getInstalledApp(db, app.ID)
	if err != nil || got.Status != statusInstalled {
		t.Fatalf("status transition: got %+v, err %v", got, err)
	}
	if err := deleteInstalledApp(db, app.ID); err != nil {
		t.Fatalf("deleteInstalledApp: %v", err)
	}
	got, err = getInstalledApp(db, app.ID)
	if err != nil || got != nil {
		t.Fatalf("deleted app should be absent: got %+v, err %v", got, err)
	}
}
