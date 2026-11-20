package fastcatv2

import (
	"context"
	stdtls "crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qhttp "github.com/metacubex/http"
	"github.com/metacubex/quic-go/http3"
	qtls "github.com/metacubex/tls"
)

func TestSharedWebsiteHTTPAndTunnel(t *testing.T) {
	dir := t.TempDir()
	content := filepath.Join(dir, "site")
	if err := os.Mkdir(content, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(file string, data []byte) {
		t.Helper()
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(content, "index.html"), []byte("<h1>CatNode test site</h1>"))
	write(filepath.Join(content, "style.css"), []byte("body { color: #444; }"))
	certFixture := httptest.NewTLSServer(http.NotFoundHandler())
	fixture := certFixture.TLS.Certificates[0]
	certFixture.Close()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	write(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.Certificate[0]}))
	keyBytes, err := x509.MarshalPKCS8PrivateKey(fixture.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	write(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))
	tokenFile := filepath.Join(dir, "tokens")
	const token = "local-integration-token-000001"
	write(tokenFile, []byte(token+"\n"))
	stopgap, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := stopgap.Addr().String()
	stopgap.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-listen", address, "-cert", cert, "-key", key,
			"-h2-tokens", tokenFile, "-h3-tokens", tokenFile, "-web-root", content}, func() { close(ready) })
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("listeners failed: %v", err)
	case <-ctx.Done():
		cancel()
		t.Fatal("listeners did not start")
	}
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.Certificate[0]}))
	addressURL := "https://" + address
	check := func(client *http.Client, path string, proto, status int, content string) *http.Response {
		t.Helper()
		resp, err := client.Get(addressURL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != status || resp.ProtoMajor != proto || !strings.Contains(string(body), content) {
			t.Fatalf("got %s %d %q (%v)", resp.Proto, resp.StatusCode, body, err)
		}
		return resp
	}
	h1 := &http.Transport{TLSClientConfig: &stdtls.Config{RootCAs: roots}}
	defer h1.CloseIdleConnections()
	check(&http.Client{Transport: h1, Timeout: 3 * time.Second}, "/", 1, 200, "CatNode test site")
	h2 := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &stdtls.Config{RootCAs: roots}}
	defer h2.CloseIdleConnections()
	h2Client := &http.Client{Transport: h2, Timeout: 3 * time.Second}
	if check(h2Client, "/", 2, 200, "CatNode test site").Header.Get("Alt-Svc") == "" {
		t.Fatal("missing website HTTP/3 advertisement")
	}
	check(h2Client, "/style.css", 2, 200, "color")
	check(h2Client, "/missing", 2, 404, "404")
	for supplied, want := range map[string]int{"": 407, token: 403} {
		req, err := http.NewRequest(http.MethodConnect, addressURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "127.0.0.1:9"
		if supplied != "" {
			req.Header.Set("Proxy-Authorization", "Bearer "+supplied)
		}
		res, err := h2Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("CONNECT: want %d got %d", want, res.StatusCode)
		}
	}
	h3 := &http3.Transport{TLSClientConfig: &qtls.Config{RootCAs: roots}}
	defer h3.Close()
	resp, err := (&qhttp.Client{Transport: h3, Timeout: 3 * time.Second}).Get(addressURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.ProtoMajor != 3 || resp.StatusCode != 200 || !strings.Contains(string(body), "CatNode test site") {
		t.Fatalf("HTTP/3: %s %d %q (%v)", resp.Proto, resp.StatusCode, body, err)
	}
	h3Client := &qhttp.Client{Transport: h3, Timeout: 3 * time.Second}
	for supplied, want := range map[string]int{"": 407, token: 403} {
		req, err := qhttp.NewRequest(qhttp.MethodConnect, addressURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "127.0.0.1:9"
		if supplied != "" {
			req.Header.Set("Proxy-Authorization", "Bearer "+supplied)
		}
		result, err := h3Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		result.Body.Close()
		if result.StatusCode != want {
			t.Fatalf("H3 CONNECT: want %d got %d", want, result.StatusCode)
		}
	}

}
