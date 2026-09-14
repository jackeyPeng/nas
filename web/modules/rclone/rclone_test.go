package rclone

import "testing"

func TestToBackendRemote_Local(t *testing.T) {
	rt, cfg, err := toBackendRemote("local", map[string]string{"local_path": "/data/nas1/public"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rt != "alias" {
		t.Errorf("rtype = %q, want alias", rt)
	}
	if cfg["remote"] != ":local:/data/nas1/public" {
		t.Errorf("remote = %q, want :local:/data/nas1/public", cfg["remote"])
	}
}

func TestToBackendRemote_LocalMissingPath(t *testing.T) {
	_, _, err := toBackendRemote("local", map[string]string{})
	if err == nil {
		t.Fatal("want error for local without local_path")
	}
}

func TestToBackendRemote_NonLocalUnchanged(t *testing.T) {
	in := map[string]string{"endpoint": "https://s3.example.com"}
	rt, cfg, err := toBackendRemote("s3", in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rt != "s3" || cfg["endpoint"] != in["endpoint"] {
		t.Errorf("s3 类型不应被改动: rt=%q cfg=%v", rt, cfg)
	}
}

func TestToFrontendRemote_AliasLocal(t *testing.T) {
	rt, cfg := toFrontendRemote("alias", map[string]string{"remote": ":local:/data/nas1/public"})
	if rt != "local" {
		t.Errorf("rtype = %q, want local", rt)
	}
	if cfg["local_path"] != "/data/nas1/public" {
		t.Errorf("local_path = %q, want /data/nas1/public", cfg["local_path"])
	}
}

func TestToFrontendRemote_AliasNonLocal(t *testing.T) {
	rt, cfg := toFrontendRemote("alias", map[string]string{"remote": "mys3:bucket"})
	if rt != "alias" || cfg["remote"] != "mys3:bucket" {
		t.Errorf("非 local 的 alias 不应被改动: rt=%q cfg=%v", rt, cfg)
	}
}

func TestToFrontendRemote_NonAlias(t *testing.T) {
	rt, cfg := toFrontendRemote("webdav", map[string]string{"url": "https://x"})
	if rt != "webdav" || cfg["url"] != "https://x" {
		t.Errorf("webdav 不应被改动: rt=%q cfg=%v", rt, cfg)
	}
}
