package fastcatv2

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
)

func Run(ctx context.Context, args []string) error {
	return run(ctx, args, func() {})
}

func run(ctx context.Context, args []string, ready func()) error {
	flags := flag.NewFlagSet("fastcat-v2", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:18446", "shared TCP and UDP address")
	cert := flags.String("cert", "", "certificate chain PEM")
	key := flags.String("key", "", "certificate private key PEM")
	h2Tokens := flags.String("h2-tokens", "", "H2 credential file")
	h3Tokens := flags.String("h3-tokens", "", "H3 credential file")
	panel := flags.String("panel", "", "HTTPS Catboard base URL")
	panelKey := flags.String("panel-token-file", "", "file with Catboard server token")
	nodeID := flags.Int("node-id", 0, "Catboard v2node ID")
	curve := flags.String("tls-key-exchange", "default", "TLS key exchange")
	if err := flags.Parse(args); err != nil {
		return helpOrError(err)
	}
	_, port, err := net.SplitHostPort(*listen)
	if err != nil || port == "" || port == "0" || *cert == "" || *key == "" ||
		flags.NArg() != 0 {
		return errors.New("a shared listen address, certificate, and key are required")
	}
	common := []string{"-listen", *listen, "-cert", *cert, "-key", *key, "-tls-key-exchange", *curve}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var h2Auth, h3Auth func([]string) (func(int), func(int), bool)
	if *panel != "" || *panelKey != "" || *nodeID != 0 {
		if *h2Tokens != "" || *h3Tokens != "" {
			return errors.New("choose panel synchronization or local credential files")
		}
		grants, err := newPanelGrants(*panel, *panelKey, *nodeID)
		if err != nil {
			return err
		}
		if err := grants.sync(ctx); err != nil {
			return fmt.Errorf("initial authorization sync: %w", err)
		}
		reporter := newTrafficReporter(grants, *nodeID)
		h2Auth, h3Auth = reporter.meter(false), reporter.meter(true)
		reportCtx, stopReporting := context.WithCancel(ctx)
		reportDone := make(chan struct{})
		go func() { defer close(reportDone); reporter.run(reportCtx) }()
		defer func() { stopReporting(); <-reportDone }()
		go grants.poll(ctx)
	} else if *h2Tokens == "" || *h3Tokens == "" {
		return errors.New("both local credential files are required")
	}
	results := make(chan error, 2)
	listeners := make(chan struct{}, 2)
	start := func(name string, run func(context.Context, []string, func(), func([]string) (func(int), func(int), bool)) error, tokens string, authorize func([]string) (func(int), func(int), bool)) {
		entryArgs := append(append([]string{}, common...), "-tokens-file", tokens)
		go func() {
			err := run(ctx, entryArgs, func() { listeners <- struct{}{} }, authorize)
			if err != nil {
				err = fmt.Errorf("%s: %w", name, err)
			}
			results <- err
		}()
	}
	start("HTTP/2", runServer, *h2Tokens, h2Auth)
	start("HTTP/3", runH3Server, *h3Tokens, h3Auth)
	for i := 0; i < 2; i++ {
		select {
		case <-listeners:
		case first := <-results:
			cancel()
			return errors.Join(first, <-results)
		}
	}
	ready()
	first := <-results
	cancel()
	return errors.Join(first, <-results)
}
