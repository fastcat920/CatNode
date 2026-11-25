package core

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fastcat920/fcn"
	tls "github.com/metacubex/tls"
	log "github.com/sirupsen/logrus"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/common/format"
	xn "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
)

func isFCN(t string) bool { return t == "fcn-v1" || t == "fcn-v2" }

type fcnLifetimeKey struct{}

type fcnAccount struct {
	key     []byte
	streams map[*context.CancelFunc]struct{}
}
type fcnNode struct {
	mu     sync.Mutex
	users  map[string]*fcnAccount
	cancel context.CancelFunc
	socket net.PacketConn
	done   chan struct{}
}

func (v *V2Core) fcnNode(tag string) *fcnNode {
	v.fcnMu.Lock()
	defer v.fcnMu.Unlock()
	return v.fcnNodes[tag]
}

func fcnCertificateConfig(ci *panel.CertInfo) (*tls.Config, error) {
	if ci.CertMode == "remote" {
		pair, err := tls.X509KeyPair([]byte(ci.TlsCert), []byte(ci.TlsKey))
		if err != nil {
			return nil, err
		}
		return &tls.Config{Certificates: []tls.Certificate{pair}}, nil
	}
	serverName := ""
	if ci.CertMode == "http" || ci.CertMode == "dns" {
		serverName = ci.CertDomain
	}
	return fcnFileCertificateConfig(ci.CertFile, ci.KeyFile, serverName, false)
}

func fcnFileCertificateConfig(certFile, keyFile, serverName string, rejectUnknownSNI bool) (*tls.Config, error) {
	load := func() (*tls.Certificate, error) {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		if pair.Leaf == nil {
			pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				return nil, err
			}
		}
		if serverName != "" {
			if err := pair.Leaf.VerifyHostname(serverName); err != nil {
				return nil, err
			}
		}
		if now := time.Now(); now.Before(pair.Leaf.NotBefore) || !now.Before(pair.Leaf.NotAfter) {
			return nil, errors.New("FCN certificate falls outside its validity interval")
		}
		return &pair, nil
	}
	current, err := load()
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	// Leaving Certificates empty also routes handshakes without SNI through
	// GetCertificate. A partial rotation is safe until the prior pair expires.
	return &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		if next, err := load(); err == nil {
			current = next
		}
		if !time.Now().Before(current.Leaf.NotAfter) {
			return nil, errors.New("FCN certificate expired")
		}
		if rejectUnknownSNI && (hello == nil || current.Leaf.VerifyHostname(hello.ServerName) != nil) {
			return nil, errors.New("FCN unknown TLS server name")
		}
		return current, nil
	}}, nil
}

func (v *V2Core) addFCN(tag string, info *panel.NodeInfo) error {
	if info.Security != panel.Tls || info.Common.CertInfo == nil {
		return fmt.Errorf("FCN requires a TLS certificate")
	}
	tlsConfig, err := fcnCertificateConfig(info.Common.CertInfo)
	if err != nil {
		return fmt.Errorf("FCN certificate: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &fcnNode{users: make(map[string]*fcnAccount), cancel: cancel, done: make(chan struct{})}
	cfg := fcn.ServerConfig{Congestion: "bbr"}
	cfg.LookupKey = func(account string) []byte {
		n.mu.Lock()
		defer n.mu.Unlock()
		if u := n.users[account]; u != nil {
			return append([]byte(nil), u.key...)
		}
		return nil
	}
	cfg.AuthorizeContext = func(parent context.Context, account string, peer net.Addr) (context.Context, func(), error) {
		n.mu.Lock()
		defer n.mu.Unlock()
		u := n.users[account]
		if u == nil {
			return nil, nil, fcn.ErrAuth
		}
		source, err := xn.ParseDestination("tcp:" + peer.String())
		if err != nil {
			return nil, nil, err
		}
		child, stop := context.WithCancel(parent)
		u.streams[&stop] = struct{}{}
		child = session.ContextWithInbound(child, &session.Inbound{Tag: tag, Name: info.Type, Source: source, User: &protocol.MemoryUser{Email: format.UserTag(tag, account)}})
		child = context.WithValue(child, fcnLifetimeKey{}, child)
		return child, func() { stop(); n.mu.Lock(); delete(u.streams, &stop); n.mu.Unlock() }, nil
	}
	cfg.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		dest, err := xn.ParseDestination(network + ":" + target)
		if err != nil {
			return nil, err
		}
		// Xray dispatch returns a lazy pipe; its context must live with the stream,
		// rather than the short target-open deadline that is canceled after return.
		lifetime, ok := ctx.Value(fcnLifetimeKey{}).(context.Context)
		if !ok || ctx.Err() != nil {
			return nil, fcn.ErrAuth
		}
		return xcore.Dial(lifetime, v.Server, dest)
	}
	server, err := fcn.NewServer(cfg)
	if err != nil {
		cancel()
		return err
	}
	n.socket, err = net.ListenPacket("udp", net.JoinHostPort(info.Common.ListenIP, strconv.Itoa(info.Common.ServerPort)))
	if err != nil {
		cancel()
		return err
	}
	v.fcnMu.Lock()
	if v.fcnNodes == nil {
		v.fcnNodes = make(map[string]*fcnNode)
	}
	if v.fcnNodes[tag] != nil {
		v.fcnMu.Unlock()
		n.socket.Close()
		cancel()
		return fmt.Errorf("FCN node already exists")
	}
	v.fcnNodes[tag] = n
	v.fcnMu.Unlock()
	go func() {
		defer close(n.done)
		defer n.socket.Close()
		if err := server.ServeQUIC(ctx, n.socket, tlsConfig); err != nil {
			log.WithField("tag", tag).WithError(err).Error("FCN listener failed")
			cancel()
		}
	}()
	return nil
}
func (n *fcnNode) close() { n.cancel(); n.socket.Close(); <-n.done }
func (v *V2Core) delFCN(tag string) bool {
	v.fcnMu.Lock()
	n := v.fcnNodes[tag]
	delete(v.fcnNodes, tag)
	v.fcnMu.Unlock()
	if n != nil {
		n.close()
		return true
	}
	return false
}
func (n *fcnNode) addUsers(users []panel.UserInfo) (int, error) {
	prepared := make(map[string]*fcnAccount, len(users))
	for _, u := range users {
		a, k, e := fcn.AccountCredentials(u.Uuid)
		if e != nil {
			return 0, e
		}
		prepared[a] = &fcnAccount{key: k, streams: make(map[*context.CancelFunc]struct{})}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for a, u := range prepared {
		if n.users[a] == nil {
			n.users[a] = u
		}
	}
	return len(prepared), nil
}
func (n *fcnNode) delUsers(users []panel.UserInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, u := range users {
		a, _, _ := fcn.AccountCredentials(u.Uuid)
		if old := n.users[a]; old != nil {
			delete(n.users, a)
			for stop := range old.streams {
				(*stop)()
			}
		}
	}
}
