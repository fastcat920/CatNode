package fastcatv2

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type trafficBatch struct {
	Node  int                 `json:"node_id"`
	ID    string              `json:"batch_id"`
	Users map[int64][2]uint64 `json:"users"`
}

type trafficReporter struct {
	grants  *panelGrants
	node    int
	path    string
	mu      sync.Mutex
	totals  map[int64][2]uint64
	pending *trafficBatch
}

func newTrafficReporter(g *panelGrants, node int) *trafficReporter {
	var path string
	if dir := os.Getenv("STATE_DIRECTORY"); dir != "" {
		path = filepath.Join(dir, fmt.Sprintf("traffic-%d.json", node))
	}
	return &trafficReporter{grants: g, node: node, path: path, totals: make(map[int64][2]uint64)}
}

func (t *trafficReporter) meter(h3 bool) func([]string) (func(int), func(int), bool) {
	return func(headers []string) (func(int), func(int), bool) {
		id, ok := t.grants.account(h3, headers)
		if !ok || id == 0 {
			return nil, nil, ok
		}
		return func(n int) { t.add(id, 0, n) }, func(n int) { t.add(id, 1, n) }, true
	}
}

func (t *trafficReporter) add(id int64, direction, n int) {
	t.mu.Lock()
	counts := t.totals[id]
	counts[direction] += uint64(n)
	t.totals[id] = counts
	t.mu.Unlock()
}

func (t *trafficReporter) nextBatch() (*trafficBatch, error) {
	if t.pending != nil {
		return t.pending, nil
	}
	if t.path != "" {
		data, err := os.ReadFile(t.path)
		if err == nil {
			var saved trafficBatch
			if err := json.Unmarshal(data, &saved); err != nil {
				return nil, err
			}
			if saved.Node != t.node || len(saved.Users) == 0 {
				return nil, errors.New("invalid saved traffic batch")
			}
			t.pending = &saved
			return &saved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.totals) == 0 {
		return nil, nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	batch := &trafficBatch{Node: t.node, ID: hex.EncodeToString(id[:]), Users: make(map[int64][2]uint64)}
	for account, counts := range t.totals {
		batch.Users[account] = counts
		if len(batch.Users) == 1000 {
			break
		}
	}
	if t.path != "" {
		data, err := json.Marshal(batch)
		if err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(t.path), ".traffic-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(data)
		syncErr := tmp.Sync()
		if err := errors.Join(writeErr, syncErr, tmp.Close()); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp.Name(), t.path); err != nil {
			return nil, err
		}
	}
	for id := range batch.Users {
		delete(t.totals, id)
	}
	t.pending = batch
	return batch, nil
}

func (t *trafficReporter) flush(ctx context.Context) error {
	batch, err := t.nextBatch()
	if err != nil || batch == nil {
		return err
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	endpoint, _, _ := strings.Cut(t.grants.endpoint, "/grants?")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/traffic", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.grants.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := t.grants.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("panel returned HTTP %d", res.StatusCode)
	}
	var reply struct {
		Data bool `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&reply); err != nil || !reply.Data {
		return errors.New("invalid panel traffic acknowledgement")
	}
	if t.path != "" {
		if err := os.Remove(t.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	log.Printf("FastCat traffic uploaded: node=%d batch=%s accounts=%d", batch.Node, batch.ID, len(batch.Users))
	t.pending = nil
	return nil
}

func (t *trafficReporter) run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := t.flush(ctx); err != nil && ctx.Err() == nil {
				log.Printf("FastCat traffic upload: %v", err)
			}
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := t.flush(final); err != nil {
				log.Printf("FastCat final traffic upload: %v", err)
			}
			cancel()
			return
		}
	}
}
