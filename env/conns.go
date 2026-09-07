package env

// usql-managed named connection store.
//
// Named connections are persisted to <configdir>/connections.yaml in the same
// shape as config.yaml's `connections:` section (string DSNs or component
// maps). Passwords are NEVER written to that file: they are kept in the OS
// keyring when one is available, and otherwise in a 0600-permission file
// <configdir>/secrets.json. Passwords are only ever materialized in memory
// (env.Variables secrets), keyed by the connection name.

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/99designs/keyring"
	"github.com/xo/dburl"
	"github.com/xo/usql/text"
	"gopkg.in/yaml.v3"
)

const (
	connStoreFile   = "connections.yaml"
	secretStoreFile = "secrets.json"
)

// connSecretKeyPrefix prefixes secret-store keys with the connection name.
const connSecretKeyPrefix = "conn:"

// keyringBackends returns the keyring backends usql is willing to use for
// password storage. Overridable in tests.
var keyringBackends = func() []keyring.BackendType {
	allowed := []keyring.BackendType{
		keyring.SecretServiceBackend,
		keyring.KeychainBackend,
		keyring.WinCredBackend,
	}
	var res []keyring.BackendType
	for _, b := range keyring.AvailableBackends() {
		if slices.Contains(allowed, b) {
			res = append(res, b)
		}
	}
	return res
}

// keyring state, resolved lazily once per process.
var (
	krOnce     sync.Once
	kr         keyring.Keyring
	krBackends []keyring.BackendType
)

// ConfigDir returns the usql configuration directory.
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, text.CommandName), nil
}

// connStorePath returns the path to the usql-managed connections file.
func connStorePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, connStoreFile), nil
}

// secretStorePath returns the path to the fallback secrets file.
func secretStorePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, secretStoreFile), nil
}

// key returns the secret-store key for a named connection.
func secretKey(name string) string {
	return connSecretKeyPrefix + name
}

// readConnStore reads the usql-managed connections file. The returned map
// holds each connection as a string (DSN), a []interface{} (driver params),
// or a map[string]interface{} (components). The second return value reports
// whether the file exists.
func readConnStore() (map[string]any, bool, error) {
	path, err := connStorePath()
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return nil, false, nil
	case err != nil:
		return nil, true, err
	}
	var doc struct {
		Connections map[string]any `yaml:"connections"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, true, fmt.Errorf("parsing %s: %w", path, err)
	}
	return doc.Connections, true, nil
}

// writeConnStore writes the usql-managed connections file atomically.
func writeConnStore(conns map[string]any) error {
	path, err := connStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(struct {
		Connections map[string]any `yaml:"connections"`
	}{conns})
	if err != nil {
		return err
	}
	content := append([]byte("# managed by "+text.CommandName+` \conns`+" -- passwords are never stored here\n"), b...)
	f, err := os.CreateTemp(filepath.Dir(path), connStoreFile+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// connEntryURL validates a raw connection entry from the connections file,
// enforcing the no-plaintext-password policy, and returns its URL string.
func connEntryURL(name string, v any) (string, error) {
	redact := func(s string) (string, error) {
		u, err := dburl.Parse(s)
		if err == nil && u.User != nil {
			if pw, _ := u.User.Password(); pw != "" {
				return "", fmt.Errorf("connection %q in %s contains a plaintext password; remove it and store the password with `\\conns`", name, connStoreFile)
			}
		}
		return s, nil
	}
	switch x := v.(type) {
	case string:
		return redact(x)
	case []interface{}:
		vals := make([]string, len(x))
		for i, s := range x {
			s, ok := s.(string)
			if !ok {
				return "", fmt.Errorf("connection %q in %s has invalid entry %v", name, connStoreFile, x)
			}
			vals[i] = s
		}
		if len(vals) == 0 {
			return "", fmt.Errorf("connection %q in %s has an empty entry", name, connStoreFile)
		}
		return redact(vals[0])
	case map[string]interface{}:
		if pass, ok := x["password"]; ok && fmt.Sprintf("%v", pass) != "" {
			return "", fmt.Errorf("connection %q in %s contains a plaintext password; remove it and store the password with `\\conns`", name, connStoreFile)
		}
		if pass, ok := x["pass"]; ok && fmt.Sprintf("%v", pass) != "" {
			return "", fmt.Errorf("connection %q in %s contains a plaintext password; remove it and store the password with `\\conns`", name, connStoreFile)
		}
		s, err := buildConnURL(x)
		if err != nil {
			return "", fmt.Errorf("connection %q in %s: %w", name, connStoreFile, err)
		}
		if s == "" {
			return "", fmt.Errorf("connection %q in %s has no protocol", name, connStoreFile)
		}
		return s, nil
	default:
		return "", fmt.Errorf("connection %q in %s has invalid entry type %T", name, connStoreFile, v)
	}
}

// LoadConns loads usql-managed named connections (and their stored passwords)
// into the current session Variables. Connections defined in the user's
// config.yaml must be loaded first; a name present in both is an error.
func LoadConns() error {
	conns, exists, err := readConnStore()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(conns)) {
		if _, ok := Vars().GetConn(name); ok {
			return fmt.Errorf("connection %q is defined both in config.yaml and in %s; remove one of them", name, connStoreFile)
		}
		// encoding is usql client config, not a URL component
		if m, ok := conns[name].(map[string]interface{}); ok {
			if enc, ok := m["encoding"]; ok {
				delete(m, "encoding")
				if s, ok := enc.(string); ok && strings.TrimSpace(s) != "" {
					Vars().SetConnEncoding(name, strings.TrimSpace(s))
				}
			}
		}
		urlstr, err := connEntryURL(name, conns[name])
		if err != nil {
			return err
		}
		if err := Vars().SetConn(name, urlstr); err != nil {
			return err
		}
		Vars().SetConnSource(name, "store")
		if pw, ok, err := ReadConnPassword(name); err != nil {
			return err
		} else if ok {
			Vars().SetSecret(name, pw)
		}
	}
	return nil
}

// buildConnURL assembles the DSN for a named connection from its component
// map. File-style databases store their location in the path component
// without a hostname; dburl.BuildURL emits scheme:/path for those, whose
// generators require an empty authority (scheme:///path), so rebuild that
// form here.
func buildConnURL(components map[string]any) (string, error) {
	urlstr, err := dburl.BuildURL(components)
	if err != nil {
		return "", err
	}
	if p, ok := components["path"].(string); ok && p != "" && p != "/" {
		if _, hasHost := components["hostname"]; !hasHost {
			if _, hasHost = components["host"]; !hasHost {
				if proto, ok := components["protocol"].(string); ok && proto != "" {
					prefix := proto + ":" + p
					if strings.HasPrefix(urlstr, prefix) {
						urlstr = proto + ":///" + strings.TrimLeft(p, "/") + strings.TrimPrefix(urlstr, prefix)
					}
				}
			}
		}
	}
	return urlstr, nil
}

// SaveConn persists a named connection: components (never containing a
// password) to connections.yaml and, when password is non-empty, the password
// to the secret store. The connection becomes available in the current
// session immediately. When password is empty any previously stored password
// is left untouched.
func SaveConn(name string, components map[string]any, password string) error {
	if err := ValidIdentifier(name); err != nil {
		return err
	}
	if _, ok := components["protocol"]; !ok {
		return fmt.Errorf("connection %q is missing protocol", name)
	}
	urlstr, err := buildConnURL(components)
	if err != nil {
		return err
	}
	if urlstr == "" {
		return fmt.Errorf("connection %q has no usable components", name)
	}
	// persist components (password-free)
	conns, _, err := readConnStore()
	if err != nil {
		return err
	}
	if conns == nil {
		conns = make(map[string]any)
	}
	conns[name] = components
	if err := writeConnStore(conns); err != nil {
		return err
	}
	// persist password
	if password != "" {
		if err := writeSecret(name, password); err != nil {
			return err
		}
	}
	// make available in the session
	if err := Vars().SetConn(name, urlstr); err != nil {
		return err
	}
	// mirror the configured encoding into the session ("" clears it)
	if enc, ok := components["encoding"].(string); ok {
		Vars().SetConnEncoding(name, strings.TrimSpace(enc))
	} else {
		Vars().SetConnEncoding(name, "")
	}
	Vars().SetConnSource(name, "store")
	if password != "" {
		Vars().SetSecret(name, password)
	}
	return nil
}

// UpdateConnPassword stores a new password for an existing named connection,
// or removes the stored password when password is empty.
func UpdateConnPassword(name string, password string) error {
	if password == "" {
		if err := removeSecret(name); err != nil {
			return err
		}
		Vars().DelSecret(name)
		return nil
	}
	if err := writeSecret(name, password); err != nil {
		return err
	}
	Vars().SetSecret(name, password)
	return nil
}

// DeleteConn removes a named connection from the store and the current
// session, including any stored password.
func DeleteConn(name string) error {
	if err := ValidIdentifier(name); err != nil {
		return err
	}
	conns, exists, err := readConnStore()
	if err != nil {
		return err
	}
	if exists && conns != nil {
		if _, ok := conns[name]; ok {
			delete(conns, name)
			if err := writeConnStore(conns); err != nil {
				return err
			}
		}
	}
	_ = removeSecret(name)
	if err := Vars().SetConn(name, ""); err != nil {
		return err
	}
	Vars().SetConnSource(name, "")
	Vars().DelSecret(name)
	return nil
}

// ConnComponents returns the parsed components of a named connection as a
// component map suitable for dburl.BuildURL, or false when the connection is
// not defined or not parseable as a URL.
func ConnComponents(name string) (map[string]any, bool) {
	vals, ok := Vars().GetConn(name)
	if !ok || len(vals) == 0 {
		return nil, false
	}
	u, err := dburl.Parse(vals[0])
	if err != nil {
		return nil, false
	}
	components := map[string]any{
		"protocol": u.Scheme,
	}
	if u.User != nil && u.User.Username() != "" {
		components["username"] = u.User.Username()
	}
	if host, port, err := net.SplitHostPort(u.Host); err == nil {
		components["hostname"] = host
		if port != "" {
			components["port"] = port
		}
	} else if u.Host != "" {
		components["hostname"] = u.Host
	}
	var db string
	switch {
	case u.Opaque != "":
		db = u.Opaque
	case u.Path != "":
		db = strings.TrimPrefix(u.Path, "/")
		if v, err := url.PathUnescape(db); err == nil {
			db = v
		}
	}
	if db != "" {
		components["database"] = db
	}
	if u.RawQuery != "" {
		components["parameters"] = u.RawQuery
	}
	return components, true
}

// MaskURL masks any password embedded in a URL string.
func MaskURL(s string) string {
	u, err := dburl.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	if pw, _ := u.User.Password(); pw == "" {
		return s
	}
	// drop the password entirely: net/url percent-encodes replacement
	// characters, so a placeholder would not survive a round trip.
	u.User = url.User(u.User.Username())
	return u.String()
}

// secretBackend opens (once) the OS keyring, returning it and whether it is
// usable.
func secretBackend() (keyring.Keyring, bool) {
	krOnce.Do(func() {
		krBackends = keyringBackends()
		if len(krBackends) == 0 {
			return
		}
		var err error
		kr, err = keyring.Open(keyring.Config{
			ServiceName:     "usql",
			AllowedBackends: krBackends,
		})
		if err != nil {
			krBackends = nil
		}
	})
	return kr, len(krBackends) != 0 && kr != nil
}

// readSecretFile reads the fallback secrets file.
func readSecretFile() (map[string]string, error) {
	path, err := secretStorePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// writeSecretFile writes the fallback secrets file with 0600 permissions.
func writeSecretFile(m map[string]string) error {
	path, err := secretStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// readSecret retrieves a stored password for a named connection, from the OS
// keyring when available, and otherwise from the fallback secrets file.
func readSecret(name string) (string, bool, error) {
	if kr, ok := secretBackend(); ok {
		item, err := kr.Get(secretKey(name))
		switch err {
		case nil:
			return string(item.Data), true, nil
		case keyring.ErrKeyNotFound:
			return "", false, nil
		default:
			// backend present but unusable (e.g. no daemon): fall through to
			// the fallback file
		}
	}
	m, err := readSecretFile()
	if err != nil {
		return "", false, err
	}
	pw, ok := m[secretKey(name)]
	return pw, ok, nil
}

// writeSecret stores a password for a named connection in the OS keyring when
// available, and otherwise in the fallback secrets file. Failures of an
// unusable keyring backend (e.g. no daemon on a headless host) fall back to
// the file.
func writeSecret(name, password string) error {
	if kr, ok := secretBackend(); ok {
		if err := kr.Set(keyring.Item{
			Key:   secretKey(name),
			Label: "usql connection " + name,
			Data:  []byte(password),
		}); err == nil {
			return nil
		}
	}
	m, err := readSecretFile()
	if err != nil {
		return err
	}
	if m == nil {
		m = make(map[string]string)
	}
	m[secretKey(name)] = password
	return writeSecretFile(m)
}

// removeSecret removes a stored password for a named connection.
func removeSecret(name string) error {
	kr, krok := secretBackend()
	if krok {
		if err := kr.Remove(secretKey(name)); err == nil {
			return nil
		}
		// fall through to the fallback file
	}
	m, err := readSecretFile()
	if err != nil {
		return err
	}
	if m == nil {
		return nil
	}
	if _, ok := m[secretKey(name)]; !ok {
		return nil
	}
	delete(m, secretKey(name))
	return writeSecretFile(m)
}

// ReadConnPassword returns the stored password for a named connection, from
// the session cache first, then from the secret store.
func ReadConnPassword(name string) (string, bool, error) {
	if pw, ok := Vars().GetSecret(name); ok {
		return pw, true, nil
	}
	return readSecret(name)
}

// SaveConnFromURL stores a successfully connected URL as a named connection,
// extracting the components and password. The URL's password (if any) is kept
// in the secret store, never in the components file. A non-empty encoding
// (the --encoding value the connection was started with) is recorded with
// the connection, so reconnecting by name re-applies it; the UTF-8 default
// is not stored.
func SaveConnFromURL(name string, u *dburl.URL, encoding string) error {
	components := map[string]any{"protocol": u.Scheme}
	if u.User != nil && u.User.Username() != "" {
		components["username"] = u.User.Username()
	}
	if host := u.Hostname(); host != "" {
		components["hostname"] = host
	}
	if port := u.Port(); port != "" {
		components["port"] = port
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		components["database"] = db
	} else if u.Opaque != "" {
		// file-style schemes keep the raw path in the opaque component
		components["path"] = u.Opaque
	}
	if enc := strings.ToLower(strings.TrimSpace(encoding)); enc != "" && enc != "utf-8" && enc != "utf8" {
		components["encoding"] = enc
	}
	password, _ := u.User.Password()
	return SaveConn(name, components, password)
}

// DefaultConnName builds a default identifier for a connected URL from its
// scheme, user, host, port, and database (when present), e.g.
// postgres_kube_127.0.0.1_5432_dev. The result is always a valid connection
// identifier (letters, digits, _), so characters like @, ., - are replaced
// with _.
func DefaultConnName(u *dburl.URL) string {
	parts := []string{u.Scheme}
	if u.User != nil && u.User.Username() != "" {
		parts = append(parts, u.User.Username())
	}
	if host := u.Hostname(); host != "" {
		parts = append(parts, host)
	}
	if port := u.Port(); port != "" {
		parts = append(parts, port)
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		parts = append(parts, db)
	}
	return sanitizeIdentifier(strings.Join(parts, "_"))
}

// sanitizeIdentifier replaces characters that are not letters, digits, or _
// with _, so the result is a valid connection name.
func sanitizeIdentifier(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return '_'
	}, s)
}
