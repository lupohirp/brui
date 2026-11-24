package bru

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeMachine(t *testing.T) {
	old := machineIDFunc
	machineIDFunc = func() (string, error) { return "0123abcd-test-machine", nil }
	t.Cleanup(func() { machineIDFunc = old })
}

// writeBrunoSecrets writes a secrets.json like the Bruno app does.
func writeBrunoSecrets(t *testing.T, collPath string, values map[string]map[string]string) string {
	t.Helper()
	f := &secretsFile{}
	for env, vars := range values {
		for name, v := range vars {
			enc, err := encryptSecret(v)
			if err != nil {
				t.Fatal(err)
			}
			f.set(collPath, env, name, enc, false)
		}
	}
	p := filepath.Join(t.TempDir(), "bruno-secrets.json")
	data, _ := json.Marshal(f)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretEncryptionRoundTrip(t *testing.T) {
	fakeMachine(t)
	for _, v := range []string{"x", "exactly16bytes!!", "pässwörd with ünicode", strings.Repeat("k", 100)} {
		enc, err := encryptSecret(v)
		if err != nil || !strings.HasPrefix(enc, "$01:") {
			t.Fatalf("encrypt %q: %v %q", v, err, enc)
		}
		got, err := (&SecretStore{}).decrypt(enc)
		if err != nil || got != v {
			t.Fatalf("round trip %q: got %q, %v", v, got, err)
		}
	}
	// Ciphertext from another machine is reported, not returned as garbage.
	enc, _ := encryptSecret("secret")
	machineIDFunc = func() (string, error) { return "another-machine", nil }
	if _, err := (&SecretStore{}).decrypt(enc); err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("want a clear error, got %v", err)
	}
}

func TestOldBrunoKeyDerivation(t *testing.T) {
	fakeMachine(t)
	id, _ := machineIDFunc()
	key, iv := evpBytesToKey([]byte(id), 32, 16)
	ct, _ := cbcEncrypt(key, iv, []byte("legacy"))
	if got, err := decryptAES256(ct); err != nil || got != "legacy" {
		t.Fatalf("legacy secret: %q %v", got, err)
	}
}

func TestSafeStorageLinuxBasicBackend(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Chromium's fixed v10 key is Linux-only")
	}
	key, _ := pbkdf2.Key(sha1.New, "peanuts", []byte("saltysalt"), 1, 16)
	ct, _ := cbcEncrypt(key, bytes.Repeat([]byte{' '}, aes.BlockSize), []byte("from-keyring-less-linux"))
	enc := "$00:" + hex.EncodeToString(append([]byte("v10"), ct...))
	if got, err := (&SecretStore{}).decrypt(enc); err != nil || got != "from-keyring-less-linux" {
		t.Fatalf("safeStorage v10: %q %v", got, err)
	}
}

const secretEnvFile = `vars {
  host: https://api.example.com
  user: alice
}
vars:secret [
  PASSWORD,
  API_KEY
]
`

func newSecretsCollection(t *testing.T, brunoValues map[string]string) (*BrunoCollection, string) {
	t.Helper()
	fakeMachine(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "environments"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "bruno.json"), []byte(`{"version":"1","name":"s","type":"collection"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "environments", "Dev.bru"), []byte(secretEnvFile), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("# comment\nexport TOKEN=\"abc\\n123\"\nPLAIN=v1 # note\nQUOTED='a # b'\n"), 0o644)
	coll, err := LoadCollection(dir)
	if err != nil {
		t.Fatal(err)
	}
	brunoPath := writeBrunoSecrets(t, dir, map[string]map[string]string{"Dev": brunoValues})
	ownPath := filepath.Join(t.TempDir(), "brui", "secrets.json")
	coll.Secrets = NewSecretStore(ownPath, brunoPath)
	coll.ActiveEnvironment = "Dev"
	return coll, ownPath
}

func TestSecretVariablesResolveFromBrunoThenBrui(t *testing.T) {
	coll, ownPath := newSecretsCollection(t, map[string]string{"PASSWORD": "from-bruno"})
	env := coll.ActiveEnv()
	if strings.Join(env.SecretNames, ",") != "PASSWORD,API_KEY" || env.IsSecret("user") {
		t.Fatalf("secret names: %v", env.SecretNames)
	}
	if v, src, ok := LookupVariable("PASSWORD", coll); !ok || v != "from-bruno" || src != SourceSecret {
		t.Fatalf("Bruno secret: %q %q %v", v, src, ok)
	}
	if _, _, ok := LookupVariable("API_KEY", coll); ok {
		t.Fatal("a secret without a value must be undefined")
	}

	// A value set in brui wins and is stored encrypted, 0600, outside the collection.
	if err := coll.Secrets.Set(coll.Path, "Dev", "PASSWORD", "from-brui"); err != nil {
		t.Fatal(err)
	}
	if v, from, _ := coll.Secrets.Value(coll.Path, "Dev", "PASSWORD"); v != "from-brui" || from != SecretFromBrui {
		t.Fatalf("brui secret: %q from %q", v, from)
	}
	data, _ := os.ReadFile(ownPath)
	info, _ := os.Stat(ownPath)
	if strings.Contains(string(data), "from-brui") || !strings.Contains(string(data), "$01:") || info.Mode().Perm() != 0o600 {
		t.Fatalf("brui store must be encrypted and private: %s (%v)", data, info.Mode())
	}
	// A new store (next brui start) reads it back.
	fresh := NewSecretStore(ownPath, "")
	if v, _, ok := fresh.Value(coll.Path, "Dev", "PASSWORD"); !ok || v != "from-brui" {
		t.Fatalf("persisted secret: %q %v", v, ok)
	}
	// Deleting it falls back to Bruno's value.
	coll.Secrets.Delete(coll.Path, "Dev", "PASSWORD")
	if v, _, _ := coll.Secrets.Value(coll.Path, "Dev", "PASSWORD"); v != "from-bruno" {
		t.Fatalf("after delete: %q", v)
	}

	// Interpolation and scripts see secrets; the request carries the value.
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotAuth = r.Header.Get("X-Pass") }))
	defer srv.Close()
	coll.Secrets.Set(coll.Path, "Dev", "API_KEY", "k-123")
	req := &BrunoRequest{Method: MethodGet, URL: srv.URL, Headers: []BruHeader{{Name: "X-Pass", Value: "{{PASSWORD}}", Enabled: true}},
		PreRequestScript: `bru.setVar("seen", bru.getEnvVar("API_KEY"))`}
	res := RunWithScripts(context.Background(), req, coll, RunOptions{})
	if res.Err != nil || gotAuth != "from-bruno" || res.RuntimeVariables["seen"] != "k-123" {
		t.Fatalf("send: err=%v header=%q script=%v", res.Err, gotAuth, res.RuntimeVariables["seen"])
	}
}

func TestSecretNamesSerialization(t *testing.T) {
	env := &BrunoEnvironment{Name: "Dev", Variables: map[string]string{}}
	*env = *parseEnvironmentString(t, secretEnvFile)
	if got := SerializeEnvironment(env); got != secretEnvFile {
		t.Fatalf("unchanged env must round-trip:\n%s", got)
	}
	// Turning a variable into a secret moves it out of vars; the value is never written.
	delete(env.Variables, "user")
	env.SetSecret("user", true)
	env.SetSecret("API_KEY", false)
	got := SerializeEnvironment(env)
	want := "vars {\n  host: https://api.example.com\n}\nvars:secret [\n  PASSWORD,\n  user\n]\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	env.SetSecret("PASSWORD", false)
	env.SetSecret("user", false)
	if got := SerializeEnvironment(env); strings.Contains(got, "vars:secret") {
		t.Fatalf("empty secret list must drop the block:\n%s", got)
	}
}

func parseEnvironmentString(t *testing.T, content string) *BrunoEnvironment {
	p := filepath.Join(t.TempDir(), "Dev.bru")
	os.WriteFile(p, []byte(content), 0o644)
	return parseEnvironmentFile(p, "Dev")
}

func TestProcessEnvFromDotEnv(t *testing.T) {
	coll, _ := newSecretsCollection(t, nil)
	t.Setenv("FROM_OS", "os-value")
	cases := map[string]string{
		"{{process.env.TOKEN}}":   "abc\n123",
		"{{process.env.PLAIN}}":   "v1",
		"{{process.env.QUOTED}}":  "a # b",
		"{{process.env.FROM_OS}}": "os-value",
		"{{process.env.MISSING}}": "{{process.env.MISSING}}",
	}
	for in, want := range cases {
		if got := Interpolate(in, coll); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
	sr := NewScriptRunner(coll)
	if err := sr.RunPreRequest(&BrunoRequest{PreRequestScript: `bru.setVar("a", process.env.PLAIN); bru.setVar("b", bru.getProcessEnv("FROM_OS"))`}); err != nil {
		t.Fatal(err)
	}
	if coll.RuntimeVariables["a"] != "v1" || coll.RuntimeVariables["b"] != "os-value" {
		t.Fatalf("scripts: %v", coll.RuntimeVariables)
	}
}
