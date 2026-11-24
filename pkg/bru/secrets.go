package bru

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
)

// Secret environment variables, compatible with Bruno.
//
// Bruno lists secret names in the environment file (`vars:secret [ A, B ]`)
// and keeps their values out of the collection, encrypted in its app data
// (secrets.json). brui reads those values, so secrets set in Bruno just work,
// and stores the ones set in brui in its own file with the same format and
// encryption, never in the .bru files.

// SourceSecret is the LookupVariable source of secret variables.
const SourceSecret = "secret"

// Where a secret value was found.
const (
	SecretFromBrui  = "brui"
	SecretFromBruno = "Bruno"
)

// secretsFile mirrors Bruno's secrets.json layout.
type secretsFile struct {
	Collections []secretsCollection `json:"collections"`
}

type secretsCollection struct {
	Path         string           `json:"path"`
	Environments []secretsEnvList `json:"environments"`
}

type secretsEnvList struct {
	Name    string        `json:"name"`
	Secrets []secretEntry `json:"secrets"`
}

type secretEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (f *secretsFile) find(collPath, env, name string) (string, bool) {
	collPath = posixPath(collPath)
	for _, c := range f.Collections {
		if posixPath(c.Path) != collPath {
			continue
		}
		for _, e := range c.Environments {
			if e.Name != env {
				continue
			}
			for _, s := range e.Secrets {
				if s.Name == name {
					return s.Value, true
				}
			}
		}
	}
	return "", false
}

func (f *secretsFile) set(collPath, env, name, value string, remove bool) {
	collPath = posixPath(collPath)
	ci := -1
	for i := range f.Collections {
		if posixPath(f.Collections[i].Path) == collPath {
			ci = i
		}
	}
	if ci < 0 {
		if remove {
			return
		}
		f.Collections = append(f.Collections, secretsCollection{Path: collPath})
		ci = len(f.Collections) - 1
	}
	c := &f.Collections[ci]
	ei := -1
	for i := range c.Environments {
		if c.Environments[i].Name == env {
			ei = i
		}
	}
	if ei < 0 {
		if remove {
			return
		}
		c.Environments = append(c.Environments, secretsEnvList{Name: env})
		ei = len(c.Environments) - 1
	}
	e := &c.Environments[ei]
	for i := range e.Secrets {
		if e.Secrets[i].Name == name {
			if remove {
				e.Secrets = append(e.Secrets[:i], e.Secrets[i+1:]...)
			} else {
				e.Secrets[i].Value = value
			}
			return
		}
	}
	if !remove {
		e.Secrets = append(e.Secrets, secretEntry{Name: name, Value: value})
	}
}

func posixPath(p string) string {
	return strings.TrimSuffix(filepath.ToSlash(filepath.Clean(p)), "/")
}

// SecretStore resolves secret values: brui's own store first, then Bruno's.
// It is safe for concurrent use. Bruno values are decrypted lazily, because
// on macOS that may ask for Keychain access.
type SecretStore struct {
	mu        sync.Mutex
	path      string // brui store; "" = in memory only
	brunoPath string // Bruno's secrets.json; "" = disabled
	own       *secretsFile
	bruno     *secretsFile
	cache     map[string]secretValue // decrypted values by collection|env|name
	keychain  map[string][]byte      // Electron safeStorage passwords
}

type secretValue struct {
	value, from string
	err         error
}

// NewSecretStore returns a store persisting brui's secrets to path and
// reading Bruno's from brunoPath (either may be "").
func NewSecretStore(path, brunoPath string) *SecretStore {
	return &SecretStore{path: path, brunoPath: brunoPath, cache: map[string]secretValue{}, keychain: map[string][]byte{}}
}

// DefaultSecretStore uses ~/.config/brui/secrets.json and Bruno's app data.
func DefaultSecretStore() *SecretStore {
	path := ""
	if dir, err := os.UserConfigDir(); err == nil {
		path = filepath.Join(dir, "brui", "secrets.json")
	}
	return NewSecretStore(path, BrunoSecretsPath())
}

// BrunoSecretsPath returns the secrets.json of the Bruno app, or "".
func BrunoSecretsPath() string {
	if p := os.Getenv("BRUI_BRUNO_SECRETS"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir() // ~/.config, ~/Library/Application Support, %AppData%
	if err != nil {
		return ""
	}
	for _, name := range []string{"bruno", "Bruno"} {
		p := filepath.Join(dir, name, "secrets.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadSecretsFile(path string) *secretsFile {
	f := &secretsFile{}
	if path == "" {
		return f
	}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, f)
	}
	return f
}

func (s *SecretStore) load() {
	if s.own == nil {
		s.own = loadSecretsFile(s.path)
	}
	if s.bruno == nil {
		s.bruno = loadSecretsFile(s.brunoPath)
	}
}

// Value returns the secret value of name in environment env of the
// collection at collPath, and where it comes from.
func (s *SecretStore) Value(collPath, env, name string) (value, from string, ok bool) {
	if s == nil {
		return "", "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := posixPath(collPath) + "\x00" + env + "\x00" + name
	if v, hit := s.cache[key]; hit {
		return v.value, v.from, v.err == nil && v.from != ""
	}
	s.load()
	var v secretValue
	if enc, found := s.own.find(collPath, env, name); found {
		v.value, v.err = s.decrypt(enc)
		v.from = SecretFromBrui
	} else if enc, found := s.bruno.find(collPath, env, name); found && enc != "" {
		v.value, v.err = s.decrypt(enc)
		v.from = SecretFromBruno
	}
	s.cache[key] = v
	return v.value, v.from, v.err == nil && v.from != ""
}

// Problem reports why a secret stored in Bruno could not be read, if so.
func (s *SecretStore) Problem(collPath, env, name string) error {
	if s == nil {
		return nil
	}
	s.Value(collPath, env, name)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache[posixPath(collPath)+"\x00"+env+"\x00"+name].err
}

// Set stores a secret value in brui's store (encrypted like Bruno does).
func (s *SecretStore) Set(collPath, env, name, value string) error {
	enc, err := encryptSecret(value)
	if err != nil {
		return err
	}
	return s.update(collPath, env, name, func(f *secretsFile) { f.set(collPath, env, name, enc, false) })
}

// Delete removes a secret value from brui's store (Bruno's is never touched).
func (s *SecretStore) Delete(collPath, env, name string) error {
	return s.update(collPath, env, name, func(f *secretsFile) { f.set(collPath, env, name, "", true) })
}

func (s *SecretStore) update(collPath, env, name string, change func(*secretsFile)) error {
	if s == nil {
		return errors.New("no secret store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.own = loadSecretsFile(s.path) // pick up changes from other brui instances
	change(s.own)
	delete(s.cache, posixPath(collPath)+"\x00"+env+"\x00"+name)
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.own, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// --- Encryption compatible with Bruno (packages/bruno-electron/src/utils/encryption.js)

// brunoMachineID reproduces node-machine-id's machineIdSync(): the sha256
// (hex) of the platform machine id.
func brunoMachineID() (string, error) {
	var id string
	switch runtime.GOOS {
	case "linux", "freebsd":
		for _, p := range []string{"/var/lib/dbus/machine-id", "/etc/machine-id"} {
			if data, err := os.ReadFile(p); err == nil {
				id, _, _ = strings.Cut(string(data), "\n")
				break
			}
		}
		if id == "" {
			id, _ = os.Hostname()
		}
	case "darwin":
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return "", fmt.Errorf("cannot read the machine id: %w", err)
		}
		_, after, ok := strings.Cut(string(out), "IOPlatformUUID")
		if !ok {
			return "", errors.New("cannot read the machine id: IOPlatformUUID not found")
		}
		id, _, _ = strings.Cut(after, "\n")
		id = strings.NewReplacer("=", "", "\"", "").Replace(id)
	default:
		return "", fmt.Errorf("secrets are not supported on %s", runtime.GOOS)
	}
	id = strings.ToLower(strings.Join(strings.Fields(id), ""))
	if id == "" {
		return "", errors.New("cannot read the machine id")
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]), nil
}

var machineIDFunc = brunoMachineID

func aesKeyFromMachine() ([]byte, error) {
	id, err := machineIDFunc()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(id))
	return key[:], nil
}

// encryptSecret produces Bruno's "$01:" format: AES-256-CBC, zero IV, key
// sha256(machine id), hex output.
func encryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	key, err := aesKeyFromMachine()
	if err != nil {
		return "", err
	}
	ct, err := cbcEncrypt(key, make([]byte, aes.BlockSize), []byte(plain))
	if err != nil {
		return "", err
	}
	return "$01:" + hex.EncodeToString(ct), nil
}

func (s *SecretStore) decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	algo, data, ok := strings.Cut(strings.TrimPrefix(enc, "$"), ":")
	if !ok || !strings.HasPrefix(enc, "$") {
		return "", errors.New("unrecognized secret format")
	}
	raw, err := hex.DecodeString(data)
	if err != nil {
		return "", fmt.Errorf("unrecognized secret format: %w", err)
	}
	switch algo {
	case "01":
		return decryptAES256(raw)
	case "00":
		return s.decryptSafeStorage(raw)
	}
	return "", fmt.Errorf("unsupported secret encryption %q", algo)
}

func decryptAES256(raw []byte) (string, error) {
	id, err := machineIDFunc()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(id))
	if pt, err := cbcDecrypt(key[:], make([]byte, aes.BlockSize), raw); err == nil && utf8.Valid(pt) {
		return string(pt), nil
	}
	// Older Bruno versions: OpenSSL EVP_BytesToKey(MD5) key and IV.
	oldKey, oldIV := evpBytesToKey([]byte(id), 32, 16)
	if pt, err := cbcDecrypt(oldKey, oldIV, raw); err == nil && utf8.Valid(pt) {
		return string(pt), nil
	}
	return "", errors.New("cannot decrypt the secret saved by Bruno (was it saved on another machine?)")
}

// decryptSafeStorage decrypts Electron safeStorage data ("$00:"), used by
// Bruno when the OS keychain is available (the default on macOS).
func (s *SecretStore) decryptSafeStorage(raw []byte) (string, error) {
	prefix := string(raw[:min(3, len(raw))])
	var password []byte
	iterations := 1
	switch {
	case runtime.GOOS == "darwin" && prefix == "v10":
		p, err := s.keychainPassword("darwin")
		if err != nil {
			return "", err
		}
		password, iterations = p, 1003
	case runtime.GOOS == "linux" && prefix == "v10":
		password = []byte("peanuts") // Chromium's fixed key when no keyring is available
	case runtime.GOOS == "linux" && prefix == "v11":
		p, err := s.keychainPassword("linux")
		if err != nil {
			return "", err
		}
		password = p
	default:
		return "", fmt.Errorf("secret encrypted with the %s keychain; set it again in brui", runtime.GOOS)
	}
	key, err := pbkdf2.Key(sha1.New, string(password), []byte("saltysalt"), iterations, 16)
	if err != nil {
		return "", err
	}
	pt, err := cbcDecrypt(key, bytes.Repeat([]byte{' '}, aes.BlockSize), raw[3:])
	if err != nil || !utf8.Valid(pt) {
		return "", errors.New("cannot decrypt the secret saved by Bruno with the system keychain")
	}
	return string(pt), nil
}

// keychainPassword reads Bruno's safeStorage password from the OS keychain
// (macOS may ask to allow access once).
func (s *SecretStore) keychainPassword(goos string) ([]byte, error) {
	if p, ok := s.keychain[goos]; ok {
		return p, nil
	}
	var out []byte
	var err error
	for _, app := range []string{"bruno", "Bruno"} {
		if goos == "darwin" {
			out, err = exec.Command("security", "find-generic-password", "-w", "-s", app+" Safe Storage").Output()
		} else {
			out, err = exec.Command("secret-tool", "lookup", "application", app).Output()
		}
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			p := bytes.TrimRight(out, "\r\n")
			s.keychain[goos] = p
			return p, nil
		}
	}
	return nil, errors.New("cannot read Bruno's key from the system keychain; set the secret again in brui")
}

func evpBytesToKey(password []byte, keyLen, ivLen int) (key, iv []byte) {
	var derived, last []byte
	for len(derived) < keyLen+ivLen {
		h := md5.New()
		h.Write(last)
		h.Write(password)
		last = h.Sum(nil)
		derived = append(derived, last...)
	}
	return derived[:keyLen], derived[keyLen : keyLen+ivLen]
}

func cbcEncrypt(key, iv, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	buf := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(buf, buf)
	return buf, nil
}

func cbcDecrypt(key, iv, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("invalid ciphertext length")
	}
	buf := append([]byte{}, ct...)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(buf, buf)
	pad := int(buf[len(buf)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(buf) {
		return nil, errors.New("bad padding")
	}
	for _, b := range buf[len(buf)-pad:] {
		if int(b) != pad {
			return nil, errors.New("bad padding")
		}
	}
	return buf[:len(buf)-pad], nil
}

// --- Environment integration

// parseSecretNames reads the names listed in a `vars:secret [ ... ]` block;
// disabled entries (~NAME) are skipped.
func parseSecretNames(content []string) []string {
	var names []string
	for _, line := range content {
		for _, part := range strings.Split(line, ",") {
			n := strings.TrimSpace(part)
			if n == "" || strings.HasPrefix(n, "~") {
				continue
			}
			names = append(names, n)
		}
	}
	return names
}

// IsSecret reports whether name is a secret variable of env.
func (env *BrunoEnvironment) IsSecret(name string) bool {
	for _, n := range env.SecretNames {
		if n == name {
			return true
		}
	}
	return false
}

// SetSecret marks name as secret (or not), keeping SecretNames ordered.
func (env *BrunoEnvironment) SetSecret(name string, secret bool) {
	if secret == env.IsSecret(name) {
		return
	}
	if secret {
		env.SecretNames = append(env.SecretNames, name)
		return
	}
	for i, n := range env.SecretNames {
		if n == name {
			env.SecretNames = append(env.SecretNames[:i:i], env.SecretNames[i+1:]...)
			return
		}
	}
}

// secretBlock renders `vars:secret` for SerializeEnvironment, reusing the
// original block when the list did not change.
func secretBlock(env *BrunoEnvironment, orig *BruBlock) *BruBlock {
	if len(env.SecretNames) == 0 {
		return nil
	}
	if orig != nil && strings.Join(parseSecretNames(orig.Content), ",") == strings.Join(env.SecretNames, ",") {
		return orig
	}
	lines := make([]string, len(env.SecretNames))
	for i, n := range env.SecretNames {
		lines[i] = "  " + n
		if i < len(env.SecretNames)-1 {
			lines[i] += ","
		}
	}
	return &BruBlock{Header: "vars:secret", Name: "vars", SubName: "secret", Array: true, Content: lines}
}

// parseDotEnvFile reads a dotenv file: KEY=VALUE lines, optional `export `,
// # comments, single/double quotes (double quotes support \n escapes).
func parseDotEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseDotEnv(string(data))
}

func parseDotEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		switch {
		case len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"':
			val = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(val[1 : len(val)-1])
		case len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'':
			val = val[1 : len(val)-1]
		default:
			if i := strings.Index(val, " #"); i >= 0 { // inline comment
				val = strings.TrimSpace(val[:i])
			}
		}
		out[key] = val
	}
	return out
}
