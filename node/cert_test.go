package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	panel "github.com/wyx2685/v2node/api/v2board"
)

func TestFCNAutomaticCertificateCache(t *testing.T) {
	now := time.Now()
	rootKey := testCertificateKey(t)
	rootProfile := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign}
	rootPEM := testCertificate(t, rootProfile, rootProfile, rootKey, rootKey)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("fixture root was rejected")
	}

	leafKey := testCertificateKey(t)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"entry.example"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	good := testCertificate(t, leaf, rootProfile, leafKey, rootKey)
	leaf.NotBefore, leaf.NotAfter = now.Add(-2*time.Hour), now.Add(-time.Minute)
	expired := testCertificate(t, leaf, rootProfile, leafKey, rootKey)
	leaf.NotBefore, leaf.NotAfter = now.Add(-time.Hour), now.Add(time.Hour)
	selfSigned := testCertificate(t, leaf, leaf, leafKey, leafKey)
	for _, tc := range []struct {
		name, domain string
		cert         []byte
		key          *ecdsa.PrivateKey
		want         bool
	}{
		{"issued pair", "entry.example", good, leafKey, true},
		{"different domain", "other.example", good, leafKey, false},
		{"missing domain", "", good, leafKey, false},
		{"expired", "entry.example", expired, leafKey, false},
		{"old self-signed", "entry.example", selfSigned, leafKey, false},
		{"mismatched key", "entry.example", good, rootKey, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			info := &panel.CertInfo{CertDomain: tc.domain, CertFile: filepath.Join(dir, "cert"), KeyFile: filepath.Join(dir, "key")}
			key, err := EncodePrivate(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			l := &Lego{config: info}
			if err := l.writeCert(&certificate.Resource{Certificate: tc.cert, PrivateKey: []byte(key)}); err != nil {
				t.Fatal(err)
			}
			if got := reusableFCNAutomaticCertificate(info, roots); got != tc.want {
				t.Fatalf("reuse certificate = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestACMEPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "certificate.key")
	account := filepath.Join(dir, "acme-account.json")
	for _, filename := range []string{key, account} {
		if err := os.WriteFile(filename, []byte("old data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	l := &Lego{config: &panel.CertInfo{CertFile: filepath.Join(dir, "certificate.cer"), KeyFile: key}}
	if err := l.writeCert(&certificate.Resource{Certificate: []byte("certificate"), PrivateKey: []byte("key")}); err != nil {
		t.Fatal(err)
	}
	u := &User{Email: "account@example.com", key: testCertificateKey(t)}
	if err := u.Save(account); err != nil {
		t.Fatal(err)
	}
	var loaded User
	if err := loaded.Load(account); err != nil || loaded.Email != u.Email {
		t.Fatalf("could not load saved ACME account: %v", err)
	}
	for _, filename := range []string{key, account} {
		stat, err := os.Stat(filename)
		if err != nil {
			t.Fatal(err)
		}
		if stat.Mode().Perm() != 0600 {
			t.Fatalf("%s: private file permissions %o, want 600", filename, stat.Mode().Perm())
		}
	}
}

func testCertificateKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testCertificate(t *testing.T, profile, parent *x509.Certificate, key, issuer *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, profile, parent, &key.PublicKey, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
