package env

// Encrypted secret store for named-connection passwords.
//
// Passwords are kept in <configdir>/secrets.enc: an AES-256-GCM encrypted
// JSON map, written atomically with 0600 permissions. The encryption key is
// either a machine-local random key file (<configdir>/secret.key, created
// automatically, path overridable with USQL_SECRETS_KEYFILE) or, when
// USQL_SECRETS_PASSPHRASE is set, a PBKDF2-SHA256-derived key from that
// passphrase with a per-file random salt. The mode is fixed when the file is
// first created and recorded in its header. The OS keyring is deliberately
// not used: it triggers per-item approval prompts on macOS and is unavailable
// on headless hosts.
//
// A pre-existing plaintext secrets.json (the former fallback store) is
// imported and renamed to secrets.json.imported on first access.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	secretEncFile    = "secrets.enc"
	secretLegacyFile = "secrets.json"
	secretImportFile = "secrets.json.imported"
	secretKeyName    = "secret.key"

	// secrets file layout: magic | version | kdf | [salt | iterations] |
	// nonce | AES-256-GCM ciphertext. The header bytes double as the GCM
	// additional authenticated data.
	secretMagic       = "USQLSECRETS"
	secretVersion     = 1
	kdfKeyfile        = 1 // random machine-local key file
	kdfPass           = 2 // PBKDF2-SHA256 of USQL_SECRETS_PASSPHRASE
	secretKeyLen      = 32
	secretSaltLen     = 16
	secretNonceLen    = 12
	secretPBKDF2Iters = 600_000
)

// Environment variables configuring the secret store.
const (
	EnvSecretsPassphrase = "USQL_SECRETS_PASSPHRASE"
	EnvSecretsKeyfile    = "USQL_SECRETS_KEYFILE"
)

// secCache holds the decrypted secret map for the process lifetime; secrets
// are read once per run, and every write updates both the cache and the file.
var secCache map[string]string

// secretEncPath returns the path of the encrypted secrets file.
func secretEncPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, secretEncFile), nil
}

// secretKeyPath returns the path of the machine-local key file.
func secretKeyPath() (string, error) {
	if s := os.Getenv(EnvSecretsKeyfile); s != "" {
		return s, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, secretKeyName), nil
}

// loadSecretKey reads the 32-byte machine key file, creating it with 0600
// permissions when missing.
func loadSecretKey() ([]byte, error) {
	path, err := secretKeyPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) != secretKeyLen {
			return nil, fmt.Errorf("%s: key file must be %d random bytes, got %d", path, secretKeyLen, len(b))
		}
		return b, nil
	case !os.IsNotExist(err):
		return nil, err
	}
	key := make([]byte, secretKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, key, 0o600)
}

// decryptSecrets parses and decrypts a secrets file body, returning the
// stored map.
func decryptSecrets(b []byte) (map[string]string, error) {
	magicLen := len(secretMagic)
	if len(b) < magicLen+2 || string(b[:magicLen]) != secretMagic {
		return nil, errors.New("not a usql secrets file")
	}
	if v := b[magicLen]; v != secretVersion {
		return nil, fmt.Errorf("unsupported secrets file version %d", v)
	}
	var key []byte
	var hdrLen int
	switch kdf := b[magicLen+1]; kdf {
	case kdfKeyfile:
		hdrLen = magicLen + 2
		k, err := loadSecretKey()
		if err != nil {
			return nil, err
		}
		key = k
	case kdfPass:
		hdrLen = magicLen + 2 + secretSaltLen + 4
		if len(b) < hdrLen {
			return nil, errors.New("truncated secrets file")
		}
		pass := os.Getenv(EnvSecretsPassphrase)
		if pass == "" {
			return nil, fmt.Errorf("%s is passphrase-protected; set %s to unlock it", secretEncFile, EnvSecretsPassphrase)
		}
		salt := b[magicLen+2 : magicLen+2+secretSaltLen]
		iters := int(binary.BigEndian.Uint32(b[magicLen+2+secretSaltLen : hdrLen]))
		k, err := pbkdf2.Key(sha256.New, pass, salt, iters, secretKeyLen)
		if err != nil {
			return nil, err
		}
		key = k
	default:
		return nil, fmt.Errorf("unknown secrets file kdf mode %d", b[magicLen+1])
	}
	if len(b) < hdrLen+secretNonceLen {
		return nil, errors.New("truncated secrets file")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, b[hdrLen:hdrLen+secretNonceLen], b[hdrLen+secretNonceLen:], b[:hdrLen])
	if err != nil {
		return nil, fmt.Errorf("decryption failed; the key file or %s does not match this %s", EnvSecretsPassphrase, secretEncFile)
	}
	m := make(map[string]string)
	if err := json.Unmarshal(pt, &m); err != nil {
		return nil, fmt.Errorf("corrupt secrets file: %w", err)
	}
	return m, nil
}

// encryptSecrets encrypts the secret map, generating the file header (and any
// new key material) for the configured mode.
func encryptSecrets(m map[string]string) ([]byte, error) {
	var hdr []byte
	var key []byte
	if pass := os.Getenv(EnvSecretsPassphrase); pass != "" {
		salt := make([]byte, secretSaltLen)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
		hdr = append(hdr, secretMagic...)
		hdr = append(hdr, secretVersion, kdfPass)
		hdr = append(hdr, salt...)
		hdr = binary.BigEndian.AppendUint32(hdr, secretPBKDF2Iters)
		k, err := pbkdf2.Key(sha256.New, pass, salt, secretPBKDF2Iters, secretKeyLen)
		if err != nil {
			return nil, err
		}
		key = k
	} else {
		hdr = append(hdr, secretMagic...)
		hdr = append(hdr, secretVersion, kdfKeyfile)
		k, err := loadSecretKey()
		if err != nil {
			return nil, err
		}
		key = k
	}
	pt, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, secretNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(hdr, nonce...)
	return gcm.Seal(out, nonce, pt, hdr), nil
}

// readSecrets returns the decrypted secret map, loading it once per process,
// and importing any legacy plaintext store on first access.
func readSecrets() (map[string]string, error) {
	if secCache != nil {
		return secCache, nil
	}
	path, err := secretEncPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		secCache = make(map[string]string)
		if err := importLegacySecrets(); err != nil {
			secCache = nil
			return nil, err
		}
		return secCache, nil
	case err != nil:
		return nil, err
	}
	m, err := decryptSecrets(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	secCache = m
	return m, nil
}

// importLegacySecrets imports a pre-existing plaintext secrets.json into the
// encrypted store, renaming the original to secrets.json.imported.
func importLegacySecrets() error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, secretLegacyFile))
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	}
	m := make(map[string]string)
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("parsing legacy %s: %w", secretLegacyFile, err)
	}
	for k, v := range m {
		secCache[k] = v
	}
	if err := writeSecrets(secCache); err != nil {
		return err
	}
	old := filepath.Join(dir, secretLegacyFile)
	imported := filepath.Join(dir, secretImportFile)
	if err := os.Rename(old, imported); err != nil {
		return err
	}
	// the plaintext copy may have been more permissive than 0600
	return os.Chmod(imported, 0o600)
}

// writeSecrets encrypts and atomically writes the secret map.
func writeSecrets(m map[string]string) error {
	path, err := secretEncPath()
	if err != nil {
		return err
	}
	b, err := encryptSecrets(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), secretEncFile+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	secCache = m
	return nil
}
