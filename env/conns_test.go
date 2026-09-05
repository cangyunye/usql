package env

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/keyring"
)

// setupConnsTest points the config dir at a temp XDG dir and forces the
// fallback (file) secret backend so tests do not depend on an OS keyring.
func setupConnsTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	old := keyringBackends
	keyringBackends = func() []keyring.BackendType { return nil }
	t.Cleanup(func() {
		keyringBackends = old
		krOnce = sync.Once{}
		kr, krBackends = nil, nil
		vars = NewDefaultVars()
	})
	vars = NewDefaultVars()
	return dir
}

func connsFile(t *testing.T) string {
	t.Helper()
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, connStoreFile)
}

func TestSaveLoadDeleteConn(t *testing.T) {
	setupConnsTest(t)
	if err := SaveConn("dev", map[string]any{
		"protocol":   "postgres",
		"username":   "kube",
		"hostname":   "localhost",
		"port":       "5432",
		"database":   "dev",
		"parameters": "sslmode=disable",
	}, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	// store file must exist, be parseable, and contain no password
	b, err := os.ReadFile(connsFile(t))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "s3cr3t") {
		t.Fatalf("store file contains plaintext password:\n%s", s)
	}
	if !strings.Contains(s, "postgres") {
		t.Fatalf("store file missing protocol:\n%s", s)
	}
	// in-session state
	if _, ok := Vars().GetConn("dev"); !ok {
		t.Fatal("dev not in session after SaveConn")
	}
	if pw, ok := Vars().GetSecret("dev"); !ok || pw != "s3cr3t" {
		t.Fatalf("session secret = %q, %v", pw, ok)
	}
	if got := Vars().GetConnSource("dev"); got != "store" {
		t.Fatalf("source = %q", got)
	}
	// reload from disk into a fresh Variables
	vars = NewDefaultVars()
	if err := LoadConns(); err != nil {
		t.Fatal(err)
	}
	if pw, ok, err := ReadConnPassword("dev"); err != nil || !ok || pw != "s3cr3t" {
		t.Fatalf("reloaded secret = %q, %v, %v", pw, ok, err)
	}
	// components round trip
	comps, ok := ConnComponents("dev")
	if !ok {
		t.Fatal("no components for dev")
	}
	if comps["hostname"] != "localhost" || comps["database"] != "dev" {
		t.Fatalf("components = %v", comps)
	}
	// delete
	vars = NewDefaultVars()
	Vars().SetConn("dev", "postgres://localhost/dev")
	if err := DeleteConn("dev"); err != nil {
		t.Fatal(err)
	}
	if _, ok := Vars().GetConn("dev"); ok {
		t.Fatal("dev still in session after DeleteConn")
	}
}

func TestLoadConnsDuplicateConfigName(t *testing.T) {
	setupConnsTest(t)
	if err := SaveConn("dup", map[string]any{"protocol": "sqlite3", "database": "x.db"}, ""); err != nil {
		t.Fatal(err)
	}
	// simulate the entry already being defined from config.yaml
	Vars().SetConn("dup", "sqlite3://x.db")
	Vars().SetConnSource("dup", "config")
	if err := LoadConns(); err == nil || !strings.Contains(err.Error(), "defined both") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestLoadConnsRejectsPlaintextPassword(t *testing.T) {
	setupConnsTest(t)
	path := connsFile(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("connections:\n  bad: postgres://u:plainpw@localhost/db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadConns(); err == nil || !strings.Contains(err.Error(), "plaintext password") {
		t.Fatalf("expected plaintext-password error, got %v", err)
	}
	// map form as well
	if err := os.WriteFile(path, []byte("connections:\n  bad2:\n    protocol: postgres\n    password: plainpw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars = NewDefaultVars()
	if err := LoadConns(); err == nil || !strings.Contains(err.Error(), "plaintext password") {
		t.Fatalf("expected plaintext-password error (map), got %v", err)
	}
}

func TestConnPasswordLifecycle(t *testing.T) {
	setupConnsTest(t)
	if err := SaveConn("svc", map[string]any{"protocol": "mysql", "hostname": "db", "database": "svc"}, ""); err != nil {
		t.Fatal(err)
	}
	if pw, ok, err := ReadConnPassword("svc"); err != nil || ok {
		t.Fatalf("unexpected secret %q, %v, %v", pw, ok, err)
	}
	if err := UpdateConnPassword("svc", "hunter2"); err != nil {
		t.Fatal(err)
	}
	if pw, ok, err := ReadConnPassword("svc"); err != nil || !ok || pw != "hunter2" {
		t.Fatalf("secret = %q, %v, %v", pw, ok, err)
	}
	// fallback file must be 0600
	dir, _ := ConfigDir()
	info, err := os.Stat(filepath.Join(dir, secretStoreFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secrets file mode = %o, want 600", perm)
	}
	if err := UpdateConnPassword("svc", ""); err != nil {
		t.Fatal(err)
	}
	if pw, ok, err := ReadConnPassword("svc"); err != nil || ok {
		t.Fatalf("secret not cleared: %q, %v, %v", pw, ok, err)
	}
}

func TestFileConnComponentsRoundTrip(t *testing.T) {
	setupConnsTest(t)
	if err := SaveConn("db", map[string]any{"protocol": "sqlite3", "path": "/tmp/x.db"}, ""); err != nil {
		t.Fatal(err)
	}
	comps, ok := ConnComponents("db")
	if !ok || comps["database"] != "/tmp/x.db" {
		t.Fatalf("components = %v, %v", comps, ok)
	}
}

func TestMaskURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://kube:s3cr3t@localhost/dev", "postgres://kube@localhost/dev"},
		{"postgres://kube@localhost/dev", "postgres://kube@localhost/dev"},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		if got := MaskURL(c.in); got != c.want {
			t.Errorf("MaskURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDumpConnMasksPasswords(t *testing.T) {
	setupConnsTest(t)
	Vars().SetConn("plain", "postgres://kube:s3cr3t@localhost/dev")
	Vars().SetConn("nopw", "sqlite3://x.db")
	var sb strings.Builder
	if err := Vars().DumpConn(&sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if strings.Contains(out, "s3cr3t") {
		t.Fatalf("DumpConn leaked password: %s", out)
	}
	if !strings.Contains(out, "plain = ") {
		t.Fatalf("DumpConn missing plain entry: %s", out)
	}
}
