package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"

	"github.com/fastcat920/fcn"
	"github.com/fastcat920/fcnv3"
	"github.com/metacubex/tls"
	log "github.com/sirupsen/logrus"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/common/format"
	"github.com/wyx2685/v2node/core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xn "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
)

type fcnV3Account struct {
	credential fcnv3.Credential
	uid        int
}

type fcnV3Node struct {
	tag      string
	server   *fcnv3.Server
	socket   net.PacketConn
	done     chan struct{}
	stopOnce sync.Once
	dns      dns.Client
	dnsSlots chan struct{}
	mu       sync.Mutex
	accounts map[string]fcnV3Account
}

func (v *V2Core) fcnV3Node(tag string) *fcnV3Node {
	v.fcnMu.Lock()
	defer v.fcnMu.Unlock()
	return v.fcnV3Nodes[tag]
}

func fcnV3Certificate(ci *panel.CertInfo) (*tls.Config, error) {
	if ci == nil || ci.CertFile == "" || ci.KeyFile == "" {
		return nil, errors.New("FCN v3 requires certificate and key files")
	}
	switch ci.CertMode {
	case "file", "dns", "http":
	default:
		return nil, errors.New("FCN v3 requires file, DNS or HTTP certificate mode")
	}
	cfg, err := fcnFileCertificateConfig(ci.CertFile, ci.KeyFile, ci.CertDomain, ci.RejectUnknownSni)
	if err != nil {
		return nil, fmt.Errorf("FCN v3 certificate: %w", err)
	}
	return cfg, nil
}

func (v *V2Core) addFCNV3(tag string, info *panel.NodeInfo) error {
	if info.Security != panel.Tls || info.Common == nil {
		return errors.New("FCN v3 requires TLS")
	}
	tlsConfig, err := fcnV3Certificate(info.Common.CertInfo)
	if err != nil {
		return err
	}
	dnsClient, ok := v.Server.GetFeature(dns.ClientType()).(dns.Client)
	if !ok {
		return errors.New("FCN v3 requires the Xray DNS feature")
	}
	n := &fcnV3Node{tag: tag, done: make(chan struct{}), dns: dnsClient, dnsSlots: make(chan struct{}, 32)}
	n.server, err = fcnv3.NewServer(fcnv3.ServerConfig{
		TLSConfig: tlsConfig, EnableUDP: true, ConfigureQUIC: fcn.ApplyQUICBBR,
		Dispatch: func(setup, lifetime context.Context, request fcnv3.DispatchRequest) (net.Conn, error) {
			return n.dispatch(v.Server, setup, lifetime, request)
		},
	})
	if err != nil {
		return err
	}
	n.socket, err = net.ListenPacket("udp", net.JoinHostPort(info.Common.ListenIP, strconv.Itoa(info.Common.ServerPort)))
	if err != nil {
		_ = n.server.Close()
		return err
	}
	v.fcnMu.Lock()
	if v.fcnV3Nodes[tag] != nil {
		v.fcnMu.Unlock()
		n.stop()
		return errors.New("FCN v3 node already exists")
	}
	if v.fcnV3Nodes == nil {
		v.fcnV3Nodes = make(map[string]*fcnV3Node)
	}
	v.fcnV3Nodes[tag] = n
	v.fcnMu.Unlock()
	go func() {
		defer close(n.done)
		defer n.stop()
		if err := n.server.Serve(n.socket); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
			log.WithField("tag", tag).WithError(err).Error("FCN v3 listener failed")
		}
	}()
	return nil
}

func (n *fcnV3Node) stop() {
	n.stopOnce.Do(func() {
		_ = n.server.Close()
		_ = n.socket.Close()
	})
}

func (n *fcnV3Node) close() {
	n.stop()
	<-n.done
}

func (v *V2Core) delFCNV3(tag string) bool {
	v.fcnMu.Lock()
	n := v.fcnV3Nodes[tag]
	delete(v.fcnV3Nodes, tag)
	v.fcnMu.Unlock()
	if n == nil {
		return false
	}
	n.close()
	return true
}

func fcnV3Accounts(users []panel.UserInfo) (map[string]fcnV3Account, error) {
	accounts := make(map[string]fcnV3Account, len(users))
	for _, user := range users {
		credential, err := fcnv3.CredentialFromUUID(user.Uuid)
		if err != nil || user.Id <= 0 {
			return nil, errors.New("invalid FCN v3 panel account")
		}
		if _, exists := accounts[credential.ID]; exists {
			return nil, errors.New("duplicate FCN v3 panel account")
		}
		accounts[credential.ID] = fcnV3Account{credential: credential, uid: user.Id}
	}
	return accounts, nil
}

// FCNV3LimiterUsers preserves panel UID and limits while using the same opaque
// user key in the limiter, dispatcher, and traffic-counter maps.
func FCNV3LimiterUsers(users []panel.UserInfo) ([]panel.UserInfo, error) {
	identities := append([]panel.UserInfo(nil), users...)
	seen := make(map[string]struct{}, len(identities))
	for i := range identities {
		credential, err := fcnv3.CredentialFromUUID(identities[i].Uuid)
		if err != nil || identities[i].Id <= 0 {
			return nil, errors.New("invalid FCN v3 panel account")
		}
		if _, exists := seen[credential.ID]; exists {
			return nil, errors.New("duplicate FCN v3 panel account")
		}
		seen[credential.ID] = struct{}{}
		identities[i].Uuid = credential.ID
	}
	return identities, nil
}

func (v *V2Core) ReplaceFCNV3Users(tag string, users []panel.UserInfo) error {
	n := v.fcnV3Node(tag)
	if n == nil {
		return errors.New("FCN v3 node not found")
	}
	accounts, err := fcnV3Accounts(users)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.publishAccounts(v.users, accounts)
}

func (n *fcnV3Node) updateAccounts(owners *UserMap, accounts map[string]fcnV3Account, remove bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	next := make(map[string]fcnV3Account, len(n.accounts)+len(accounts))
	for id, account := range n.accounts {
		next[id] = account
	}
	for id, account := range accounts {
		if remove {
			delete(next, id)
		} else {
			next[id] = account
		}
	}
	return n.publishAccounts(owners, next)
}

// Caller holds n.mu, which also holds a newly authenticated request at the
// dispatch boundary until its UID is ready. Old UID mappings stay available
// to report bytes already counted when an account was revoked or rotated.
func (n *fcnV3Node) publishAccounts(owners *UserMap, next map[string]fcnV3Account) error {
	owners.mapLock.Lock()
	defer owners.mapLock.Unlock()
	credentials := make([]fcnv3.Credential, 0, len(next))
	for id, account := range next {
		if prior := owners.uidMap[format.UserTag(n.tag, id)]; prior != 0 && prior != account.uid {
			return errors.New("FCN v3 account UUID cannot be assigned to a different UID")
		}
		credentials = append(credentials, account.credential)
	}
	if err := n.server.ReplaceCredentials(credentials); err != nil {
		return err
	}
	for id, account := range next {
		owners.uidMap[format.UserTag(n.tag, id)] = account.uid
	}
	n.accounts = next
	return nil
}

var fcnV3ReservedTargetRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func fcnV3PublicTarget(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range fcnV3ReservedTargetRanges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func (n *fcnV3Node) resolve(setup context.Context, domain string) ([]net.IP, error) {
	select {
	case n.dnsSlots <- struct{}{}:
	case <-setup.Done():
		return nil, setup.Err()
	}
	type result struct {
		ips []net.IP
		err error
	}
	completed := make(chan result, 1)
	go func() {
		defer func() { <-n.dnsSlots }()
		// Xray's DNS feature carries the configured server and domain policies.
		ips, _, err := n.dns.LookupIP(domain, dns.IPOption{IPv4Enable: true, IPv6Enable: true})
		completed <- result{ips, err}
	}()
	select {
	case result := <-completed:
		return result.ips, result.err
	case <-setup.Done():
		return nil, setup.Err()
	}
}

func (n *fcnV3Node) dispatch(v *xcore.Instance, setup, lifetime context.Context, request fcnv3.DispatchRequest) (net.Conn, error) {
	if setup.Err() != nil || lifetime.Err() != nil {
		return nil, context.Canceled
	}
	n.mu.Lock()
	_, authorized := n.accounts[request.Account]
	n.mu.Unlock()
	if !authorized || request.Peer == nil || request.Network != "tcp" && request.Network != "udp" {
		return nil, fcnv3.ErrRejected
	}
	// Count the authenticated peer toward the device/IP limit for both tunnel
	// types. Xray uses Source.Network as its UDP-source accounting switch.
	source, err := xn.ParseDestination("tcp:" + request.Peer.String())
	if err != nil || !source.IsValid() || !source.Address.Family().IsIP() {
		return nil, fcnv3.ErrRejected
	}
	target, err := xn.ParseDestination(request.Network + ":" + request.Target)
	if err != nil || !target.IsValid() || target.Port == 0 {
		return nil, fcnv3.ErrRejected
	}
	lifetime = session.ContextWithInbound(lifetime, &session.Inbound{
		Tag: n.tag, Name: "fcn-v3", Source: source,
		User: &protocol.MemoryUser{Email: format.UserTag(n.tag, request.Account)},
	})
	if target.Address.Family().IsDomain() {
		answers, err := n.resolve(setup, target.Address.Domain())
		if err != nil || len(answers) == 0 {
			return nil, fcnv3.ErrRejected
		}
		for _, answer := range answers {
			ip, _ := netip.AddrFromSlice(answer)
			if !fcnV3PublicTarget(ip) {
				return nil, fcnv3.ErrRejected
			}
		}
		// Keep the domain available to Xray routing while pinning the actual
		// outbound target to the screened DNS answer.
		lifetime = session.ContextWithOutbounds(lifetime, []*session.Outbound{{RouteTarget: target}})
		target.Address = xn.IPAddress(answers[0])
	} else {
		ip, _ := netip.AddrFromSlice(target.Address.IP())
		if !fcnV3PublicTarget(ip) {
			return nil, fcnv3.ErrRejected
		}
	}
	if err := setup.Err(); err != nil {
		return nil, err
	}
	lifetime, writer := dispatcher.WithUplinkWriter(lifetime)
	conn, err := xcore.Dial(lifetime, v, target)
	if err != nil {
		return nil, err
	}
	if setup.Err() != nil || lifetime.Err() != nil || *writer == nil {
		_ = conn.Close()
		return nil, fcnv3.ErrRejected
	}
	return &fcnV3DispatchConn{Conn: conn, uplink: *writer, udp: target.Network == xn.Network_UDP}, nil
}

type fcnV3DispatchConn struct {
	net.Conn
	uplink buf.Writer
	udp    bool
}

func (c *fcnV3DispatchConn) CloseWrite() error { return common.Close(c.uplink) }

func (c *fcnV3DispatchConn) Write(payload []byte) (int, error) {
	if !c.udp {
		return c.Conn.Write(payload)
	}
	if len(payload) > fcnv3.MaxUDPPayload {
		return 0, fcnv3.ErrUDPPayloadTooLarge
	}
	packet := buf.New()
	_, _ = packet.Write(payload)
	if err := c.uplink.WriteMultiBuffer(buf.MultiBuffer{packet}); err != nil {
		return 0, err
	}
	return len(payload), nil
}
