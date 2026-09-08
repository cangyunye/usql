package env

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const aliasesTestYAML = `common:
  s1: select * from $tablename limit 20;
  "bad name": select 1;
  empty:
postgres:
  s1: select * from pg_tables;
  sessions: select * from pg_stat_activity;
mysql:
  sessions: show processlist;
`

func loadTestAliases(t *testing.T, content string) *AliasStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aliases.yaml")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("USQL_ALIASES", path)
	if err := LoadAliases(); err != nil {
		t.Fatal(err)
	}
	return Aliases()
}

func TestLoadAliases(t *testing.T) {
	a := loadTestAliases(t, aliasesTestYAML)
	// common alias
	if sql, scope, ok := a.Get("", "s1"); !ok || sql != "select * from $tablename limit 20;" || scope != commonSection {
		t.Errorf("common s1 got %q, %q, %v", sql, scope, ok)
	}
	// driver-specific overrides common
	if sql, scope, ok := a.Get("postgres", "s1"); !ok || sql != "select * from pg_tables;" || scope != "postgres" {
		t.Errorf("postgres s1 got %q, %q, %v", sql, scope, ok)
	}
	// driver not defined for alias falls back to common
	if sql, scope, ok := a.Get("mysql", "s1"); !ok || sql != "select * from $tablename limit 20;" || scope != commonSection {
		t.Errorf("mysql s1 got %q, %q, %v", sql, scope, ok)
	}
	// driver-only alias is invisible when not connected
	if _, _, ok := a.Get("", "sessions"); ok {
		t.Error("sessions should not be visible when not connected")
	}
	if sql, scope, ok := a.Get("mysql", "sessions"); !ok || sql != "show processlist;" || scope != "mysql" {
		t.Errorf("mysql sessions got %q, %q, %v", sql, scope, ok)
	}
	// invalid names and empty SQL are skipped
	if _, _, ok := a.Get("", "bad name"); ok {
		t.Error("invalid name should be skipped")
	}
	if _, _, ok := a.Get("", "empty"); ok {
		t.Error("empty alias should be skipped")
	}
	// unknown alias
	if _, _, ok := a.Get("postgres", "nope"); ok {
		t.Error("unknown alias should not be found")
	}
	// names merge driver and common sections (invalid/empty entries skipped)
	if names := a.Names("postgres"); !slices.Equal(names, []string{"s1", "sessions"}) {
		t.Errorf("postgres names got %v", names)
	}
}

func TestLoadAliasesMissingFile(t *testing.T) {
	a := loadTestAliases(t, "")
	if _, _, ok := a.Get("", "s1"); ok {
		t.Error("missing file should yield empty store")
	}
	if a.Path() == "" {
		t.Error("path should be recorded even when missing")
	}
}

func TestLoadAliasesKeepsPreviousOnError(t *testing.T) {
	loadTestAliases(t, aliasesTestYAML)
	if _, _, ok := Aliases().Get("postgres", "sessions"); !ok {
		t.Fatal("expected sessions to load")
	}
	broken := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(broken, []byte("common: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USQL_ALIASES", broken)
	if err := LoadAliases(); err == nil {
		t.Fatal("expected parse error")
	}
	if _, _, ok := Aliases().Get("postgres", "sessions"); !ok {
		t.Error("previous store should be kept when loading fails")
	}
}

func TestExpandPlaceholders(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		args   []string
		want   string
		wantAt int
	}{
		{
			name:   "no placeholders, no args",
			sql:    "show processlist;",
			args:   nil,
			want:   "show processlist;",
			wantAt: len("show processlist;"),
		},
		{
			name:   "no placeholders, extra args clamped",
			sql:    "show processlist;",
			args:   []string{"a", "b"},
			want:   "show processlist;",
			wantAt: len("show processlist;"),
		},
		{
			name:   "one placeholder, unfilled",
			sql:    "select * from $tablename limit 20;",
			args:   nil,
			want:   "select * from $tablename limit 20;",
			wantAt: len("select * from "),
		},
		{
			name:   "one placeholder, filled",
			sql:    "select * from $tablename limit 20;",
			args:   []string{"t_user"},
			want:   "select * from t_user limit 20;",
			wantAt: len("select * from t_user limit 20;"),
		},
		{
			name:   "two placeholders, one filled",
			sql:    "select * from $schema.$table;",
			args:   []string{"public"},
			want:   "select * from public.$table;",
			wantAt: len("select * from public."),
		},
		{
			name:   "rune index after multibyte runes",
			sql:    "select '会话' from $tablename;",
			args:   nil,
			want:   "select '会话' from $tablename;",
			wantAt: len([]rune("select '会话' from ")),
		},
		{
			name:   "numeric placeholders are not matched",
			sql:    "select * from t where id = $1 and x = $name;",
			args:   []string{"abc"},
			want:   "select * from t where id = $1 and x = abc;",
			wantAt: len("select * from t where id = $1 and x = abc;"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, at := ExpandPlaceholders(tt.sql, tt.args)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if at != tt.wantAt {
				t.Errorf("cursor got %d, want %d", at, tt.wantAt)
			}
			if at < 0 || at > len([]rune(got)) {
				t.Errorf("cursor %d out of range for %q", at, got)
			}
		})
	}
}

func TestCountPlaceholders(t *testing.T) {
	if n := CountPlaceholders("select * from $a join $b on x = $1"); n != 2 {
		t.Errorf("got %d, want 2", n)
	}
	if n := CountPlaceholders("select * from t"); n != 0 {
		t.Errorf("got %d, want 0", n)
	}
}

func TestCollapseLine(t *testing.T) {
	if got := CollapseLine("select 1\n  from t\r\n\twhere x = 'a  b';"); got != "select 1 from t where x = 'a  b';" {
		t.Errorf("got %q", got)
	}
	if got := CollapseLine("  already flat  "); got != "already flat" {
		t.Errorf("got %q", got)
	}
}
