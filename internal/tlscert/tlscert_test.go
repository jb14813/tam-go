package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsurePairGeneratesAUsableCertificate(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")

	created, err := EnsurePair(cert, key, []string{"localhost", "tam.lan", "127.0.0.1", "::1", ""})
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatalf("the pair must load: %v", err)
	}
	block, _ := pem.Decode(mustRead(t, cert))
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.DNSNames) != 2 || parsed.DNSNames[0] != "localhost" || len(parsed.IPAddresses) != 2 {
		t.Fatalf("names = %v ips = %v", parsed.DNSNames, parsed.IPAddresses)
	}
	if parsed.NotAfter.Before(time.Now().AddDate(9, 0, 0)) {
		t.Fatalf("certificate should last ten years, ends %v", parsed.NotAfter)
	}
	if info, _ := os.Stat(key); info.Mode().Perm()&0o077 != 0 && os.Getenv("OS") != "Windows_NT" {
		t.Fatalf("key file must not be world readable: %v", info.Mode())
	}

	// A second call keeps the existing pair.
	before := mustRead(t, cert)
	created, err = EnsurePair(cert, key, []string{"other"})
	if err != nil || created {
		t.Fatalf("second call: created=%v err=%v", created, err)
	}
	if string(before) != string(mustRead(t, cert)) {
		t.Fatal("second call must not rewrite the certificate")
	}

	// tam-client's remote mode skips verification; a verifying client rejects it.
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	ts.StartTLS()
	defer ts.Close()
	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	res, err := insecure.Get(ts.URL)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("serving with the generated pair: %v", err)
	}
	res.Body.Close()
	if _, err := (&http.Client{}).Get(ts.URL); err == nil {
		t.Fatal("a verifying client must reject the self-signed certificate")
	}
}

func TestEnsurePairRefusesHalfAPair(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	os.WriteFile(cert, []byte("stale"), 0o644)
	if _, err := EnsurePair(cert, key, []string{"localhost"}); err == nil {
		t.Fatal("a certificate without its key must be reported, not silently replaced")
	}
}

// TestFilesMakesAPairOnlyInTheDataDirectory: without -cert and -key the
// pair is server.crt and server.key in the data directory, made on the
// first start and kept after.
func TestFilesMakesAPairOnlyInTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	cert, key, created, err := Files(dir, "", "", []string{"localhost"})
	if err != nil || !created || cert != filepath.Join(dir, "server.crt") || key != filepath.Join(dir, "server.key") {
		t.Fatalf("first start = %q %q created=%v err=%v", cert, key, created, err)
	}
	if _, _, created, err = Files(dir, "", "", []string{"localhost"}); err != nil || created {
		t.Fatalf("second start: created=%v err=%v", created, err)
	}
}

// TestFilesNamedMustExist: -cert and -key name the user's own certificate.
// When those files are missing, a new self-signed pair there would be
// served in its place without a word; the start fails instead and names
// the file, and nothing is written anywhere.
func TestFilesNamedMustExist(t *testing.T) {
	dir, mine := t.TempDir(), t.TempDir()
	cert, key := filepath.Join(mine, "tam.example.crt"), filepath.Join(mine, "tam.example.key")
	_, _, _, err := Files(dir, cert, key, []string{"localhost"})
	if err == nil || !strings.Contains(err.Error(), "-cert") || !strings.Contains(err.Error(), cert) {
		t.Fatalf("missing -cert file: err = %v, want one naming -cert and %s", err, cert)
	}
	for _, path := range []string{cert, key, filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s was written: %v", path, err)
		}
	}

	// A real pair at those paths is served as it is.
	if _, err := EnsurePair(cert, key, []string{"tam.example"}); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, cert)
	got, gotKey, created, err := Files(dir, cert, key, []string{"localhost"})
	if err != nil || created || got != cert || gotKey != key || string(mustRead(t, cert)) != string(before) {
		t.Fatalf("named pair = %q %q created=%v err=%v", got, gotKey, created, err)
	}

	// A missing key is named too, and one flag alone is refused.
	os.Remove(key)
	if _, _, _, err := Files(dir, cert, key, nil); err == nil || !strings.Contains(err.Error(), "-key") || !strings.Contains(err.Error(), key) {
		t.Fatalf("missing -key file: err = %v", err)
	}
	if _, _, _, err := Files(dir, cert, "", nil); err == nil || !strings.Contains(err.Error(), "-key") {
		t.Fatalf("-cert without -key: err = %v", err)
	}
	if _, _, _, err := Files(dir, "", key, nil); err == nil || !strings.Contains(err.Error(), "-cert") {
		t.Fatalf("-key without -cert: err = %v", err)
	}
}

// TestFilesThatAreNoPairAreAnError: files that do not load as a pair stop
// the start with the reason, instead of every client failing to connect.
func TestFilesThatAreNoPairAreAnError(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key")
	os.WriteFile(cert, []byte("not a certificate"), 0o644)
	os.WriteFile(key, []byte("not a key"), 0o600)
	if _, _, _, err := Files(dir, cert, key, nil); err == nil {
		t.Fatal("files that are no certificate and key must be refused")
	}
	os.WriteFile(filepath.Join(dir, "server.crt"), []byte("stale"), 0o644)
	os.WriteFile(filepath.Join(dir, "server.key"), []byte("stale"), 0o600)
	if _, _, _, err := Files(dir, "", "", nil); err == nil {
		t.Fatal("a broken pair in the data directory must be refused")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
