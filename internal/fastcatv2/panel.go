package fastcatv2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const grantPollInterval = 5 * time.Second

type panelGrantSnapshot struct {
	h2, h3  map[[sha256.Size]byte]struct{}
	expires time.Time
	mode    string
}

type panelGrants struct {
	client   *http.Client
	endpoint string
	token    string
	active   atomic.Pointer[panelGrantSnapshot]
}

func newPanelGrants(base, keyFile string, node int) (*panelGrants, error) {
	endpoint, err := url.Parse(base)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" || keyFile == "" || node < 1 {
		return nil, errors.New("panel synchronization requires an HTTPS panel URL, positive node ID and server token file")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid panel server token")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/server/fastCat/grants"
	endpoint.RawQuery = url.Values{"node_id": {strconv.Itoa(node)}}.Encode()
	return &panelGrants{
		client:   &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: endpoint.String(),
		token:    token,
	}, nil
}

func (g *panelGrants) sync(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-FastCat-Transport-Modes", "1")
	res, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("panel returned HTTP %d", res.StatusCode)
	}
	const maxSnapshot = 16 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxSnapshot+1))
	if err != nil {
		return err
	}
	if len(data) > maxSnapshot {
		return errors.New("panel authorization snapshot too large")
	}
	var snapshot struct {
		Mode string   `json:"transport_mode"`
		H2   []string `json:"h2"`
		H3   []string `json:"h3"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.Mode != "" && snapshot.Mode != "auto" && snapshot.Mode != "h2" && snapshot.Mode != "h3" {
		return errors.New("panel returned an unknown transport mode")
	}
	if snapshot.H2 == nil || snapshot.H3 == nil || len(snapshot.H2) != len(snapshot.H3) {
		return errors.New("panel authorization snapshot has invalid lists")
	}
	read := func(tokens []string) (map[[sha256.Size]byte]struct{}, error) {
		hashes := make(map[[sha256.Size]byte]struct{}, len(tokens))
		for _, token := range tokens {
			_, err := hex.DecodeString(token)
			if len(token) != 64 || err != nil || token != strings.ToLower(token) {
				return nil, errors.New("panel returned an invalid credential")
			}
			hash := sha256.Sum256([]byte(token))
			if _, exists := hashes[hash]; exists {
				return nil, errors.New("panel returned a duplicate credential")
			}
			hashes[hash] = struct{}{}
		}
		return hashes, nil
	}
	h2, err := read(snapshot.H2)
	if err != nil {
		return err
	}
	h3, err := read(snapshot.H3)
	if err != nil {
		return err
	}
	g.active.Store(&panelGrantSnapshot{h2: h2, h3: h3, expires: time.Now().Add(45 * time.Second), mode: snapshot.Mode})
	return nil
}

func (g *panelGrants) poll(ctx context.Context) {
	ticker := time.NewTicker(grantPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := g.sync(ctx); err != nil && ctx.Err() == nil {
				log.Printf("FastCat panel authorization refresh: %v", err)
			}
		}
	}
}

func (g *panelGrants) authorize(h3 bool) func([]string) bool {
	return func(values []string) bool {
		if len(values) != 1 {
			return false
		}
		scheme, token, ok := strings.Cut(values[0], " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) != 64 {
			return false
		}
		snapshot := g.active.Load()
		if snapshot == nil || !time.Now().Before(snapshot.expires) {
			return false
		}
		if (h3 && snapshot.mode == "h2") || (!h3 && snapshot.mode == "h3") {
			return false
		}
		hashes := snapshot.h2
		if h3 {
			hashes = snapshot.h3
		}
		_, allowed := hashes[sha256.Sum256([]byte(token))]
		return allowed
	}
}
