package fastcatv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrafficRetryUsesSameBatchAndAccounts(t *testing.T) {
	var batches []trafficBatch
	h2 := strings.Repeat("a", 64)
	h3 := strings.Repeat("b", 64)
	panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("missing panel authentication")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/v1/server/fastCat/grants":
			json.NewEncoder(w).Encode(map[string]any{"h2": []string{h2}, "h3": []string{h3}, "account_ids": []int64{71}})
		case "/api/v1/server/fastCat/traffic":
			var batch trafficBatch
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Error(err)
				return
			}
			batches = append(batches, batch)
			if len(batches) == 1 {
				w.WriteHeader(503)
				return
			}
			w.Write([]byte(`{"data":true}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer panel.Close()
	g := &panelGrants{client: panel.Client(), endpoint: panel.URL + "/api/v1/server/fastCat/grants?node_id=55", token: "test"}
	ctx := context.Background()
	if err := g.sync(ctx); err != nil {
		t.Fatal(err)
	}
	r := newTrafficReporter(g, 55)
	r.path = filepath.Join(t.TempDir(), "pending.json")
	up, down, ok := r.meter(false)([]string{"Bearer " + h2})
	if !ok || up == nil {
		t.Fatal("missing H2 accounting")
	}
	up(10)
	down(20)
	up, down, ok = r.meter(true)([]string{"Bearer " + h3})
	if !ok || up == nil {
		t.Fatal("missing H3 accounting")
	}
	up(4)
	down(6)
	if r.flush(ctx) == nil {
		t.Fatal("expected failed upload")
	}
	restart := newTrafficReporter(g, 55)
	restart.path = r.path
	if err := restart.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || batches[0].ID != batches[1].ID || batches[1].Node != 55 ||
		batches[1].Users[71] != [2]uint64{14, 26} {
		t.Fatalf("unexpected reports: %#v", batches)
	}
}
