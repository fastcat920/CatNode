package node

import (
	"context"
	"encoding/json"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/limiter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFailedTrafficBatchRetainedUntilSuccess(t *testing.T) {
	var bodies []map[string][]int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/UniProxy/push" {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		if r.URL.Query().Get("node_type") != "v2node" || r.URL.Query().Get("node_id") != "92" {
			t.Error("wrong panel identity")
		}
		var body map[string][]int64
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	retries := 0
	api, err := panel.New(&conf.NodeConfig{APIHost: server.URL, NodeID: 92, Key: "test-only", RetryCount: &retries})
	if err != nil {
		t.Fatal(err)
	}
	limiter.Init()
	l := limiter.AddLimiter("fcn-v2", "test", nil, map[int]int{})
	c := &Controller{apiClient: api, limiter: l, info: &panel.NodeInfo{Common: &panel.CommonNode{}}, pendingTraffic: []panel.UserTraffic{{UID: 42, Upload: 123, Download: 456}}}
	if err := c.reportUserTrafficTask(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.pendingTraffic) != 1 {
		t.Fatal("failed batch was discarded")
	}
	if err := c.reportUserTrafficTask(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.pendingTraffic) != 0 || len(bodies) != 2 || !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatal("failed batch did not retry identically")
	}
	if !reflect.DeepEqual(bodies[1], map[string][]int64{"42": {123, 456}}) {
		t.Fatalf("wrong account counters: %+v", bodies)
	}
}
