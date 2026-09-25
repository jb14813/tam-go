package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
