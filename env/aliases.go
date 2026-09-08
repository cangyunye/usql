package env

// usql-managed SQL alias store.
//
// Aliases are user-defined named SQL templates read at startup from
// <configdir>/aliases.yaml (overridable with USQL_ALIASES). The file has a
// `common:` section with aliases available everywhere, plus optional
// per-driver sections (postgres:, mysql:, ...) whose aliases are only visible
// while connected to that driver; a driver section overrides common entries
// of the same name. Alias SQL may contain $name placeholders, which \alias
// expands into the input line for editing.

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/xo/usql/text"
	"gopkg.in/yaml.v3"
)

// aliasStoreFile is the usql-managed aliases file name.
const aliasStoreFile = "aliases.yaml"

// commonSection is the aliases.yaml section holding driver-independent
// aliases.
const commonSection = "common"

// placeholderRE matches $name placeholders in alias SQL templates. Numeric
// placeholders ($1, $2, ...) — e.g. PostgreSQL positional parameters — are
// deliberately not matched.
var placeholderRE = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)

// lineGapRE matches a newline and its surrounding whitespace.
var lineGapRE = regexp.MustCompile(`\s*\r?\n\s*`)

// aliases is the process-global alias store.
var aliases = newAliasStore("")

// Alias describes a SQL alias definition.
type Alias struct {
	// Name is the alias name.
	Name string
	// SQL is the alias SQL template.
	SQL string
	// Scope is the defining section: a driver name or "common".
	Scope string
}

// AliasStore is the loaded SQL alias store.
type AliasStore struct {
	path     string
	common   map[string]string
	byDriver map[string]map[string]string
}

// newAliasStore creates an empty alias store for path.
func newAliasStore(path string) *AliasStore {
	return &AliasStore{
		path:     path,
		common:   make(map[string]string),
		byDriver: make(map[string]map[string]string),
	}
}

// Aliases returns the global SQL alias store.
func Aliases() *AliasStore {
	return aliases
}

// Path returns the aliases file path the store was loaded from, or "" when
// unknown.
func (a *AliasStore) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// AliasFile returns the path to the aliases file.
//
// Defaults to <configdir>/aliases.yaml, overridden by environment variable
// <COMMAND NAME>_ALIASES (ie, USQL_ALIASES).
func AliasFile() (string, error) {
	if s, ok := Getenv(text.CommandUpper() + "_ALIASES"); ok {
		return s, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, aliasStoreFile), nil
}

// aliasDoc is the aliases.yaml document shape: a common section, plus
// per-driver sections. Any top-level mapping other than "common" names a
// driver.
type aliasDoc struct {
	Common  map[string]string            `yaml:"common"`
	Drivers map[string]map[string]string `yaml:",inline"`
}

// LoadAliases loads the process-global alias store from the aliases file. A
// missing file yields an empty store; the previous store is kept when loading
// fails. Entries with an invalid name or no SQL are skipped with a warning.
func LoadAliases() error {
	path, err := AliasFile()
	if err != nil {
		return err
	}
	a := newAliasStore(path)
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		aliases = a
		return nil
	case err != nil:
		return err
	}
	var doc aliasDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	delete(doc.Drivers, commonSection)
	loadAliasSection(a.common, path, commonSection, doc.Common)
	for driver, m := range doc.Drivers {
		a.byDriver[driver] = make(map[string]string)
		loadAliasSection(a.byDriver[driver], path, driver, m)
	}
	aliases = a
	return nil
}

// loadAliasSection validates and copies entries into dst, warning about and
// skipping invalid ones.
func loadAliasSection(dst map[string]string, path, section string, m map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(m)) {
		sql := strings.TrimSpace(m[name])
		switch {
		case ValidIdentifier(name) != nil:
			fmt.Fprintf(os.Stderr, text.InvalidAliasEntry+"\n", path,
				fmt.Errorf("%s: invalid alias name %q", section, name))
		case sql == "":
			fmt.Fprintf(os.Stderr, text.InvalidAliasEntry+"\n", path,
				fmt.Errorf("%s: alias %q has no SQL", section, name))
		default:
			dst[name] = sql
		}
	}
}

// Get returns the SQL for alias name as visible for the given driver, with
// driver-specific definitions overriding common ones. scope reports the
// defining section (a driver name or "common").
func (a *AliasStore) Get(driver, name string) (sql, scope string, ok bool) {
	if a == nil {
		return "", "", false
	}
	if driver != "" {
		if m := a.byDriver[driver]; m != nil {
			if sql, ok = m[name]; ok {
				return sql, driver, true
			}
		}
	}
	sql, ok = a.common[name]
	return sql, commonSection, ok
}

// Names returns the sorted alias names visible for the given driver.
func (a *AliasStore) Names(driver string) []string {
	if a == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(a.common))
	for name := range a.common {
		seen[name] = struct{}{}
	}
	if driver != "" {
		for name := range a.byDriver[driver] {
			seen[name] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// List returns the aliases visible for the given driver, sorted by name, with
// driver-specific definitions overriding common ones.
func (a *AliasStore) List(driver string) []Alias {
	names := a.Names(driver)
	res := make([]Alias, 0, len(names))
	for _, name := range names {
		sql, scope, _ := a.Get(driver, name)
		res = append(res, Alias{Name: name, SQL: sql, Scope: scope})
	}
	return res
}

// ExpandPlaceholders substitutes args into the first placeholders of sql,
// positionally. It returns the expanded sql and the rune index of the first
// remaining placeholder — the end of the string when none remain — suitable
// for cursor placement.
func ExpandPlaceholders(sql string, args []string) (string, int) {
	matches := placeholderRE.FindAllStringIndex(sql, -1)
	n := min(len(args), len(matches))
	var b strings.Builder
	last, remaining := 0, -1
	for i, m := range matches {
		switch {
		case i < n:
			b.WriteString(sql[last:m[0]])
			b.WriteString(args[i])
			last = m[1]
		case remaining == -1:
			remaining = b.Len() + m[0] - last
		}
	}
	b.WriteString(sql[last:])
	out := b.String()
	if remaining == -1 {
		return out, len([]rune(out))
	}
	return out, len([]rune(out[:remaining]))
}

// CountPlaceholders returns the number of $placeholders in sql.
func CountPlaceholders(sql string) int {
	return len(placeholderRE.FindAllStringIndex(sql, -1))
}

// CollapseLine folds newlines and their surrounding whitespace into single
// spaces, so multi-line templates fit on the single interactive input line.
func CollapseLine(s string) string {
	return lineGapRE.ReplaceAllString(strings.TrimSpace(s), " ")
}
