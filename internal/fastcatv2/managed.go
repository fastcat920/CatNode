package fastcatv2

import (
	"context"
	"errors"
	"strconv"
)

type Settings struct {
	Listen         string
	Certificate    string
	PrivateKey     string
	H2Tokens       string
	H3Tokens       string
	TLSKeyExchange string
	Panel          string
	PanelTokenFile string
	NodeID         int
}

func (s Settings) Start(ctx context.Context) (<-chan error, error) {
	if s.Listen == "" || s.Certificate == "" || s.PrivateKey == "" {
		return nil, errors.New("FastCatV2 requires Listen, Certificate, and PrivateKey")
	}
	args := []string{
		"-listen", s.Listen, "-cert", s.Certificate, "-key", s.PrivateKey,
		"-h2-tokens", s.H2Tokens, "-h3-tokens", s.H3Tokens, "-tls-key-exchange", s.TLSKeyExchange,
		"-panel", s.Panel, "-panel-token-file", s.PanelTokenFile, "-node-id", strconv.Itoa(s.NodeID),
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, args, func() { close(ready) })
	}()
	select {
	case <-ready:
		return done, nil
	case err := <-done:
		return nil, errors.Join(errors.New("FastCat v2 stopped before startup"), err)
	}
}
