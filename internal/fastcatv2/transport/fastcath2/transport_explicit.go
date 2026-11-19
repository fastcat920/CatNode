package h2client

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	qtls "github.com/metacubex/tls"
	"github.com/wyx2685/v2node/internal/fastcatv2/component/fastcatprobe"
	"github.com/wyx2685/v2node/internal/fastcatv2/component/fastcattls"
	"github.com/wyx2685/v2node/internal/fastcatv2/component/fastcattls/quicprofile"
	"golang.org/x/net/http2"
)

type explicitConn struct {
	net.Conn
	security fastcattls.Negotiated
}

func explicitTransport(config Config, dial func(context.Context, string, string) (net.Conn, error)) (transport, error) {
	cfg, err := quicprofile.ClientConfig(config.ServerName, config.RootCAs, "h2", config.CurveMode)
	if err != nil {
		return nil, err
	}
	return &http2.Transport{
		StrictMaxConcurrentStreams: true, IdleConnTimeout: 30 * time.Second,
		MaxHeaderListSize: 16 << 10, DisableCompression: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			raw, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			client := qtls.Client(raw, cfg)
			timing := fastcatprobe.From(ctx)
			timing.Mark(fastcatprobe.GatewayTLSStart)
			if err = client.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			timing.Mark(fastcatprobe.GatewayTLSDone)
			return &explicitConn{client, quicprofile.Security(client.ConnectionState())}, nil
		},
	}, nil
}
