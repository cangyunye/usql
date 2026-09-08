package env

// One-time import of passwords stored in the OS keyring by earlier builds.
//
// The encrypted secrets file (secrets.go) replaced the OS keyring as the
// password store: keyring access triggers per-item approval prompts on macOS,
// keyring items cannot be inspected or backed up from the CLI, and no keyring
// is available on headless hosts. MigrateKeyringPasswords is the only usql
// code path that touches the keyring.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/99designs/keyring"
)

// MigrateKeyringPasswords moves stored passwords for named connections out of
// the OS keyring (service "usql", keys "conn:<name>") into the encrypted
// secrets file, removing each keyring item after it has been stored. It
// returns the migrated connection names. The OS may ask the user to approve
// reading each keyring item; this is a one-time cost.
func MigrateKeyringPasswords() ([]string, error) {
	kr, err := keyring.Open(keyring.Config{ServiceName: "usql"})
	if err != nil {
		return nil, err
	}
	keys, err := kr.Keys()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, k := range keys {
		if !strings.HasPrefix(k, connSecretKeyPrefix) {
			continue
		}
		item, err := kr.Get(k)
		if err != nil {
			return names, fmt.Errorf("reading %q from keyring: %w", k, err)
		}
		name := strings.TrimPrefix(k, connSecretKeyPrefix)
		if err := writeSecret(name, string(item.Data)); err != nil {
			return names, fmt.Errorf("storing %q: %w", name, err)
		}
		if err := kr.Remove(k); err != nil {
			return names, fmt.Errorf("removing %q from keyring: %w", k, err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}
