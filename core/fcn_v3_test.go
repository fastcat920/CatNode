package core

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fastcat920/fcnv3"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/limiter"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/transport/pipe"
)

func fcnV3TestCertificate(t *testing.T, serial int64) (cert, key []byte, root *x509.Certificate) {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	profile := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "entry.example"},
		DNSNames: []string{"entry.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, profile, profile, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), root
}

func writeFCNV3TestFile(t *testing.T, name string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(name, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestFCNV3CertificateRotation(t *testing.T) {
	dir := t.TempDir()
	ci := &panel.CertInfo{CertMode: "file", CertDomain: "entry.example", CertFile: filepath.Join(dir, "cert"), KeyFile: filepath.Join(dir, "key")}
	cert, key, _ := fcnV3TestCertificate(t, 1)
	writeFCNV3TestFile(t, ci.CertFile, cert)
	writeFCNV3TestFile(t, ci.KeyFile, key)
	cfg, err := fcnV3Certificate(ci)
	if err != nil {
		t.Fatal(err)
	}
	newCert, newKey, _ := fcnV3TestCertificate(t, 2)
	writeFCNV3TestFile(t, ci.KeyFile, newKey)
	retained, err := cfg.GetCertificate(nil)
	if err != nil || retained.Leaf.SerialNumber.Int64() != 1 {
		t.Fatalf("partial rotation lost the previous pair: %v", err)
	}
	writeFCNV3TestFile(t, ci.CertFile, newCert)
	rotated, err := cfg.GetCertificate(nil)
	if err != nil || rotated.Leaf.SerialNumber.Int64() != 2 {
		t.Fatalf("valid rotation was not published: %v", err)
	}
}

func TestFCNV3DispatchHalfClose(t *testing.T) {
	uplinkReader, uplinkWriter := pipe.New()
	downlink, remote := net.Pipe()
	defer downlink.Close()
	defer remote.Close()
	conn := &fcnV3DispatchConn{Conn: downlink, uplink: uplinkWriter}
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := uplinkReader.ReadMultiBuffer(); !errors.Is(err, io.EOF) {
		t.Fatalf("the dispatcher uplink did not receive EOF: %v", err)
	}
	go func() { _, _ = remote.Write([]byte("response")) }()
	var result [8]byte
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(conn, result[:]); err != nil || string(result[:]) != "response" {
		t.Fatalf("closing the write half interrupted the downlink: %q %v", result, err)
	}
}

type fcnV3TestDNS struct{ dns.Client }

func (fcnV3TestDNS) LookupIP(domain string, _ dns.IPOption) ([]net.IP, uint32, error) {
	switch domain {
	case "origin.example", "blocked.example":
		return []net.IP{net.ParseIP("93.184.216.34")}, 20, nil
	case "private.example":
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")}, 20, nil
	default:
		return nil, 0, dns.ErrEmptyResponse
	}
}

func TestFCNV3PanelDispatchTrafficAndRevocation(t *testing.T) {
	tcpOrigin, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpOrigin.Close()
	udpOrigin, err := net.ListenPacket("udp4", tcpOrigin.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer udpOrigin.Close()
	go func() {
		for {
			conn, err := tcpOrigin.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var mode [1]byte
				if _, err := io.ReadFull(conn, mode[:]); err != nil {
					return
				}
				if mode[0] == '!' {
					var data [len("half-close")]byte
					if _, err := io.ReadFull(conn, data[:]); err == nil {
						_, _ = conn.Write(data[:])
					}
					return
				}
				_, _ = conn.Write(mode[:])
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	go func() {
		var b [2048]byte
		for {
			n, source, err := udpOrigin.ReadFrom(b[:])
			if err != nil {
				return
			}
			_, _ = udpOrigin.WriteTo(b[:n], source)
		}
	}()

	cert, key, root := fcnV3TestCertificate(t, 1)
	dir := t.TempDir()
	ci := &panel.CertInfo{CertMode: "file", CertDomain: "entry.example", CertFile: filepath.Join(dir, "cert"), KeyFile: filepath.Join(dir, "key")}
	writeFCNV3TestFile(t, ci.CertFile, cert)
	writeFCNV3TestFile(t, ci.KeyFile, key)
	// An explicit test-only router detour makes the pin-checked destinations
	// terminate at the loopback echo fixtures instead of leaving the host.
	redirect := fmt.Sprintf(`{"protocol":"freedom","tag":"fcn-v3-fixture","settings":{"redirect":%q,"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]},{"action":"block","delay":0}]}}`, tcpOrigin.Addr().String())
	const tag = "fcn-v3-test"
	info := &panel.NodeInfo{Type: "fcn-v3", Tag: tag, Security: panel.Tls, Common: &panel.CommonNode{
		ListenIP: "127.0.0.1", CertInfo: ci,
		Routes: []panel.Route{{Action: "block", Match: []string{"full:blocked.example"}}, {Action: "default_out", ActionValue: &redirect}},
	}}
	user := panel.UserInfo{Id: 42, Uuid: "1ed909de-848b-41e7-943f-0f2d49b35f3a"}
	users := []panel.UserInfo{user}
	for i := 1; i <= 256; i++ {
		users = append(users, panel.UserInfo{Id: 100 + i, Uuid: fmt.Sprintf("00000000-0000-4000-8000-%012x", i)})
	}
	identities, err := FCNV3LimiterUsers(users)
	if err != nil {
		t.Fatal(err)
	}
	limiter.Init()
	limiter.AddLimiter(info.Type, tag, identities, map[int]int{})
	defer limiter.DeleteLimiter(tag)
	v := New(conf.New())
	if err := v.Start([]*panel.NodeInfo{info}); err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.AddNode(tag, info); err != nil {
		t.Fatal(err)
	}
	n := v.fcnV3Node(tag)
	n.dns = fcnV3TestDNS{}
	if err := v.ReplaceFCNV3Users(tag, users); err != nil {
		t.Fatal(err)
	}
	credential, err := fcnv3.CredentialFromUUID(user.Uuid)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	client, err := fcnv3.NewClient(fcnv3.Config{Endpoint: n.socket.LocalAddr().String(), ServerName: "entry.example", Roots: pool,
		Account: credential.ID, Secret: credential.Secret, EnableUDP: true, AllowDomainTargets: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	probeTCP := func(target string) net.Conn {
		t.Helper()
		conn, err := client.Dial(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		return conn
	}
	closedWrite := probeTCP("origin.example:443")
	defer closedWrite.Close()
	if _, err := closedWrite.Write([]byte("!half-close")); err != nil {
		t.Fatal(err)
	}
	if err := closedWrite.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(closedWrite); err != nil || string(got) != "half-close" {
		t.Fatalf("closing the client write half lost the origin response: %q %v", got, err)
	}
	stream := probeTCP("93.184.216.34:443")
	defer stream.Close()
	if _, err := stream.Write([]byte("account-TCP")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	if _, err := io.ReadFull(stream, buf[:11]); err != nil || string(buf[:11]) != "account-TCP" {
		t.Fatalf("invalid metered TCP echo: %q %v", buf[:11], err)
	}
	udp, err := client.DialUDP(ctx, "origin.example:443")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	for _, payload := range [][]byte{bytes.Repeat([]byte{'a'}, 12), bytes.Repeat([]byte{'b'}, fcnv3.MaxUDPPayload)} {
		if err := udp.SendDatagram(payload); err != nil {
			t.Fatal(err)
		}
		if received, err := udp.ReceiveDatagram(ctx); err != nil || !bytes.Equal(received, payload) {
			t.Fatalf("invalid metered UDP echo: %d bytes %v", len(received), err)
		}
	}
	for _, target := range []string{"127.0.0.1:443", "private.example:443", "blocked.example:443"} {
		bad := probeTCP(target)
		_, _ = bad.Write([]byte("denied"))
		if _, err := bad.Read(buf); err == nil {
			t.Errorf("target %s unexpectedly reached an origin", target)
		}
		_ = bad.Close()
	}
	traffic, err := v.GetUserTrafficSlice(tag, 0)
	want := int64(11 + 11 + 12 + fcnv3.MaxUDPPayload)
	if err != nil || len(traffic) != 1 || traffic[0].UID != 42 || traffic[0].Upload != want || traffic[0].Download != want-1 {
		t.Fatalf("want UID 42 and %d/%d payload bytes: %+v %v", want, want-1, traffic, err)
	}
	if more, err := v.GetUserTrafficSlice(tag, 0); err != nil || len(more) != 0 {
		t.Fatalf("traffic was counted twice: %+v %v", more, err)
	}
	if _, err := stream.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(stream, buf[:4]); err != nil || string(buf[:4]) != "tail" {
		t.Fatalf("invalid final TCP echo: %q %v", buf[:4], err)
	}
	if err := v.ReplaceFCNV3Users(tag, []panel.UserInfo{}); err != nil {
		t.Fatal(err)
	}
	traffic, err = v.GetUserTrafficSlice(tag, 0)
	if err != nil || len(traffic) != 1 || traffic[0] != (panel.UserTraffic{UID: 42, Upload: 4, Download: 4}) {
		t.Fatalf("revocation lost final traffic: %+v %v", traffic, err)
	}
	_ = stream.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := stream.Read(buf); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("TCP revocation did not close live stream promptly: %v", err)
	}
	stop, cancelStop := context.WithTimeout(ctx, time.Second)
	defer cancelStop()
	if _, err := udp.ReceiveDatagram(stop); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("UDP revocation did not close live association promptly: %v", err)
	}
	if _, err := client.DialUDP(ctx, "origin.example:443"); !errors.Is(err, fcnv3.ErrRejected) {
		t.Fatalf("revoked UUID opened a new UDP association: %v", err)
	}
	denied := probeTCP("origin.example:443")
	defer denied.Close()
	if _, err := denied.Read(buf); !errors.Is(err, fcnv3.ErrRejected) {
		t.Fatalf("revoked UUID opened a new TCP stream: %v", err)
	}
}
