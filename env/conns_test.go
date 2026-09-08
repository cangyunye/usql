package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xo/dburl"
)

// setupConnsTest points the config dir at a temp XDG dir. HOME is redirected
// as well: os.UserConfigDir ignores XDG_CONFIG_HOME on darwin, and without it
// the tests would read the user's real connections file.
func setupConnsTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv(EnvSecretsPassphrase, "")
	t.Setenv(EnvSecretsKeyfile, "")
	t.Cleanup(func() {
		secCache = nil
		vars = NewDefaultVars()
	})
	secCache = nil
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
	// secrets file and key file must be 0600
	dir, _ := ConfigDir()
	info, err := os.Stat(filepath.Join(dir, secretEncFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secrets file mode = %o, want 600", perm)
	}
	info, err = os.Stat(filepath.Join(dir, secretKeyName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode = %o, want 600", perm)
	}
	if err := UpdateConnPassword("svc", ""); err != nil {
		t.Fatal(err)
	}
	if pw, ok, err := ReadConnPassword("svc"); err != nil || ok {
		t.Fatalf("secret not cleared: %q, %v, %v", pw, ok, err)
	}
}

func TestSecretsEncryptedAtRest(t *testing.T) {
	setupConnsTest(t)
	if err := SaveConn("dev", map[string]any{
		"protocol": "postgres",
		"hostname": "db",
	}, "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, secretEncFile))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, "s3cr3t") {
		t.Fatalf("secrets file contains plaintext password:\n%s", s)
	} else if !strings.HasPrefix(s, secretMagic) {
		t.Fatalf("secrets file missing magic header:\n%q", s[:min(16, len(s))])
	}
	if kdf := b[len(secretMagic)+1]; kdf != kdfKeyfile {
		t.Fatalf("default kdf mode = %d, want %d", kdf, kdfKeyfile)
	}
	// tampering must be detected
	b[len(b)-1] ^= 1
	if err := os.WriteFile(filepath.Join(dir, secretEncFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	secCache, vars = nil, NewDefaultVars()
	if _, _, err := ReadConnPassword("dev"); err == nil {
		t.Fatal("expected tampered secrets file to fail")
	}
}

func TestSecretsPassphraseMode(t *testing.T) {
	setupConnsTest(t)
	t.Setenv(EnvSecretsPassphrase, "master pw")
	if err := SaveConn("dev", map[string]any{
		"protocol": "postgres",
		"hostname": "db",
	}, "pw1"); err != nil {
		t.Fatal(err)
	}
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, secretEncFile))
	if err != nil {
		t.Fatal(err)
	}
	if kdf := b[len(secretMagic)+1]; kdf != kdfPass {
		t.Fatalf("passphrase kdf mode = %d, want %d", kdf, kdfPass)
	}
	// correct passphrase round trips across a fresh "process"
	secCache, vars = nil, NewDefaultVars()
	if pw, ok, err := ReadConnPassword("dev"); err != nil || !ok || pw != "pw1" {
		t.Fatalf("secret = %q, %v, %v", pw, ok, err)
	}
	// wrong passphrase fails
	t.Setenv(EnvSecretsPassphrase, "wrong pw")
	secCache, vars = nil, NewDefaultVars()
	if _, _, err := ReadConnPassword("dev"); err == nil {
		t.Fatal("expected wrong passphrase to fail")
	}
	// missing passphrase fails with a hint
	t.Setenv(EnvSecretsPassphrase, "")
	secCache, vars = nil, NewDefaultVars()
	_, _, err = ReadConnPassword("dev")
	if err == nil || !strings.Contains(err.Error(), EnvSecretsPassphrase) {
		t.Fatalf("expected %s hint, got %v", EnvSecretsPassphrase, err)
	}
}

func TestLegacySecretsImport(t *testing.T) {
	setupConnsTest(t)
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, secretLegacyFile), []byte(`{"conn:old":"legacy pw"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if pw, ok, err := ReadConnPassword("old"); err != nil || !ok || pw != "legacy pw" {
		t.Fatalf("secret = %q, %v, %v", pw, ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, secretEncFile)); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(dir, secretImportFile)
	if _, err := os.Stat(imported); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, secretLegacyFile)); !os.IsNotExist(err) {
		t.Fatalf("legacy file still present: %v", err)
	}
	info, err := os.Stat(imported)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("imported file mode = %o, want 600", perm)
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

func TestBuildConnURLFilePaths(t *testing.T) {
	// file-style databases: path without a hostname must produce an empty
	// authority (scheme:///path), whose generators accept the URL
	urlstr, err := buildConnURL(map[string]any{"protocol": "sqlite", "path": "/tmp/x.db"})
	if err != nil {
		t.Fatalf("sqlite path: %v", err)
	}
	if urlstr != "sqlite:///tmp/x.db" {
		t.Fatalf("sqlite path url: %q", urlstr)
	}
	// server-style databases keep the host form
	urlstr, err = buildConnURL(map[string]any{"protocol": "postgres", "hostname": "db", "port": "5432", "database": "mydb"})
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	if urlstr != "postgres://db:5432/mydb" {
		t.Fatalf("postgres url: %q", urlstr)
	}
}

func TestSaveConnFromURLEncoding(t *testing.T) {
	setupConnsTest(t)
	u, err := dburl.Parse("mysql://kube:pw@localhost:3306/dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveConnFromURL("enc1", u, "GBK"); err != nil {
		t.Fatal(err)
	}
	if enc, ok := Vars().GetConnEncoding("enc1"); !ok || enc != "gbk" {
		t.Fatalf("session encoding = %q, %v; want gbk", enc, ok)
	}
	b, err := os.ReadFile(connsFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "encoding: gbk") {
		t.Fatalf("store file missing encoding: gbk:\n%s", b)
	}
	if strings.Contains(string(b), "pw") {
		t.Fatalf("store file leaked password:\n%s", b)
	}
	// utf-8 (the default) and empty are not recorded
	if err := SaveConnFromURL("enc2", u, "utf-8"); err != nil {
		t.Fatal(err)
	}
	if enc, ok := Vars().GetConnEncoding("enc2"); ok && enc != "" {
		t.Fatalf("utf-8 recorded as %q, want unset", enc)
	}
	if err := SaveConnFromURL("enc3", u, ""); err != nil {
		t.Fatal(err)
	}
	if enc, ok := Vars().GetConnEncoding("enc3"); ok && enc != "" {
		t.Fatalf("empty recorded as %q, want unset", enc)
	}
}
