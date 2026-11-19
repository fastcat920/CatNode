package fastcatv2

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPanelGrantRefresh(t *testing.T) {
	h2 := strings.Repeat("a", 64)
	h3 := strings.Repeat("b", 64)
	response := `{"h2":["` + h2 + `"],"h3":["` + h3 + `"]}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.URL.Query().Get("node_id") != "55" ||
			r.URL.Path != "/api/v1/server/fastCat/grants" {
			http.Error(w, "unexpected request", http.StatusForbidden)
			return
		}
		io.WriteString(w, response)
	}))
	defer server.Close()
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	grants, err := newPanelGrants(server.URL, key, 55)
	if err != nil {
		t.Fatal(err)
	}
	grants.client = server.Client()
	testAuth := func(h3 bool, token string) bool {
		return grants.authorize(h3)([]string{"Bearer " + token})
	}
	if err := grants.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !testAuth(false, h2) || !testAuth(true, h3) || testAuth(true, h2) || testAuth(false, h3) {
		t.Fatal("credentials must only work on their assigned entrance")
	}
	response = `{"h2":[],"h3":["broken"]}`
	if grants.sync(context.Background()) == nil || !testAuth(false, h2) || !testAuth(true, h3) {
		t.Fatal("invalid refresh must retain the previous snapshot")
	}
	response = `{"h2":[],"h3":[]}`
	if err := grants.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if testAuth(false, h2) || testAuth(true, h3) {
		t.Fatal("removed credentials still authorized")
	}
}
