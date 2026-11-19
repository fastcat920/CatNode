package fastcatv2

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wyx2685/v2node/internal/fastcatv2/component/fastcattls"
	"github.com/wyx2685/v2node/internal/fastcatv2/h2server"
)

func runServer(ctx context.Context, args []string, ready func(), authorize func([]string) (func(int), func(int), bool)) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	curve := flags.String("tls-key-exchange", "", "default, x25519 or x25519mlkem768")
	listen := flags.String("listen", "127.0.0.1:18444", "TLS entry address")
	certFile := flags.String("cert", "", "certificate chain PEM")
	keyFile := flags.String("key", "", "certificate private key PEM")
	tokensFile := flags.String("tokens-file", "", "one device token per line, reload on SIGHUP (default: FASTCAT_H2_TOKEN)")
	private := flags.Bool("allow-private-targets", false, "allow private destinations for loopback-only tests")
	maxTunnels := flags.Int("max-tunnels", 128, "maximum active tunnels across all clients")
	session := flags.Duration("session-timeout", 30*time.Minute, "absolute tunnel lifetime")
	if err := flags.Parse(args); err != nil {
		return helpOrError(err)
	}
	if flags.NArg() != 0 || *maxTunnels < 1 || *session <= 0 {
		return errors.New("invalid server arguments or limits")
	}
	if *private && !loopbackAddress(*listen) {
		return errors.New("private targets require a loopback test listener")
	}
	cfg := h2server.Config{Meter: authorize,
		TokensFile: *tokensFile, MaxTunnels: *maxTunnels,
		AllowPrivateTargets: *private, SessionTimeout: *session,
	}
	if *tokensFile == "" {
		cfg.Token = os.Getenv("FASTCAT_H2_TOKEN")
	}
	handler, err := h2server.New(cfg)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}
	var protocols http.Protocols
	protocols.SetHTTP2(true)
	server := &http.Server{
		Handler: logFirstH2Request(handler), Protocols: &protocols, ReadHeaderTimeout: 8 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}, Certificates: []tls.Certificate{cert}},
		// Each TLS connection gets its own context. Streams sharing it emit just one ready event.
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, entryConnKey{}, &entryConn{})
		},
	}
	if err := fastcattls.Apply(server.TLSConfig, *curve); err != nil {
		return err
	}
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer l.Close()
	stopReload := tokenReload(ctx, *tokensFile, handler.(interface{ ReloadTokens() error }))
	defer stopReload()

	log.Printf("server listen=%s", l.Addr())
	ready()
	return serveHTTP(ctx, server, func() error { return server.ServeTLS(l, "", "") })
}

func tokenReload(ctx context.Context, path string, handler interface{ ReloadTokens() error }) func() {
	if path == "" {
		return func() {}
	}
	reloads := make(chan os.Signal, 1)
	signal.Notify(reloads, syscall.SIGHUP)
	active, cancel := context.WithCancel(ctx)
	go func() {
		for {
			select {
			case <-active.Done():
				return
			case <-reloads:
				if err := handler.ReloadTokens(); err != nil {
					log.Printf("device token reload failed: %v", err)
				} else {
					log.Print("device tokens reloaded")
				}
			}
		}
	}()
	return func() { signal.Stop(reloads); cancel() }
}

type entryConnKey struct{}
type entryConn struct{ once sync.Once }

func logFirstH2Request(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && r.TLS != nil {
			conn := r.Context().Value(entryConnKey{}).(*entryConn)
			conn.once.Do(func() { log.Print("entry negotiated=h2") })
		}
		next.ServeHTTP(w, r)
	})
}

func helpOrError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && net.ParseIP(host).IsLoopback()
}

func serveHTTP(ctx context.Context, server *http.Server, serve func() error) error {
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	if err := serve(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
