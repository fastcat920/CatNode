package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fastcat920/fcn"
	tls "github.com/metacubex/tls"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/limiter"
)

func TestFCNCertificateRotationWithoutSNI(t *testing.T) {
	dir := t.TempDir()
	ci := &panel.CertInfo{CertMode: "http", CertDomain: "entry.example", CertFile: filepath.Join(dir, "cert"), KeyFile: filepath.Join(dir, "key")}
	cert, key, root := fcnV3TestCertificate(t, 1)
	nextCert, nextKey, nextRoot := fcnV3TestCertificate(t, 2)
	writeFCNV3TestFile(t, ci.CertFile, cert)
	writeFCNV3TestFile(t, ci.KeyFile, key)
	serverConfig, err := fcnCertificateConfig(ci)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig.MinVersion = tls.VersionTLS13
	serverConfig.SessionTicketsDisabled = true
	roots := x509.NewCertPool()
	roots.AddCert(root)
	roots.AddCert(nextRoot)
	// An IP server name is verified by the client but omitted from TLS SNI.
	// These fixture certificates are verified explicitly against the test roots
	// because their SAN is the configured DNS identity.
	clientConfig := &tls.Config{
		ServerName: "127.0.0.1", MinVersion: tls.VersionTLS13, InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: ci.CertDomain, Roots: roots})
			return err
		},
	}
	check := func(want int64) {
		t.Helper()
		s, c := net.Pipe()
		defer s.Close()
		defer c.Close()
		deadline := time.Now().Add(3 * time.Second)
		_ = s.SetDeadline(deadline)
		_ = c.SetDeadline(deadline)
		server := tls.Server(s, serverConfig)
		client := tls.Client(c, clientConfig)
		done := make(chan error, 1)
		go func() { done <- server.Handshake() }()
		if err := client.Handshake(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := server.ConnectionState().ServerName; got != "" {
			t.Fatalf("unexpected SNI %q", got)
		}
		if serial := client.ConnectionState().PeerCertificates[0].SerialNumber.Int64(); serial != want {
			t.Fatalf("certificate serial %d, want %d", serial, want)
		}
	}
	check(1)
	writeFCNV3TestFile(t, ci.KeyFile, nextKey)
	check(1)
	writeFCNV3TestFile(t, ci.CertFile, nextCert)
	check(2)
}

func TestFCNAccountTrafficAndRevocation(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			uuid := "1ed909de-848b-41e7-943f-0f2d49b35f3a"
			user := panel.UserInfo{Id: 42, Uuid: uuid}
			certServer := httptest.NewTLSServer(nil)
			certServer.Close()
			cert := certServer.TLS.Certificates[0]
			key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
			keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
			reservation, _ := net.ListenPacket("udp4", "127.0.0.1:0")
			port := reservation.LocalAddr().(*net.UDPAddr).Port
			reservation.Close()
			tag := "fcn-test"
			info := &panel.NodeInfo{Type: "fcn-v" + strconv.Itoa(version), Tag: tag, Security: panel.Tls, Common: &panel.CommonNode{ListenIP: "127.0.0.1", ServerPort: port, CertInfo: &panel.CertInfo{CertMode: "remote", TlsCert: string(certPEM), TlsKey: string(keyPEM)}}}
			limiter.Init()
			limiter.AddLimiter(info.Type, tag, []panel.UserInfo{user}, map[int]int{})
			vc := New(conf.New())
			if err := vc.Start([]*panel.NodeInfo{info}); err != nil {
				t.Fatal(err)
			}
			defer vc.Close()
			if err := vc.AddNode(tag, info); err != nil {
				t.Fatal(err)
			}
			if _, err := vc.AddUsers(&AddUsersParams{Tag: tag, NodeInfo: info, Users: []panel.UserInfo{user}}); err != nil {
				t.Fatal(err)
			}
			cfg, err := fcn.AccountConfig(version, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), "example.com", uuid)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Roots = x509.NewCertPool()
			parsed, _ := x509.ParseCertificate(cert.Certificate[0])
			cfg.Roots.AddCert(parsed)
			client, err := fcn.NewClient(context.Background(), cfg, "quic")
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			echo, _ := net.Listen("tcp4", "127.0.0.1:0")
			defer echo.Close()
			go func() {
				for {
					c, e := echo.Accept()
					if e != nil {
						return
					}
					go func() { defer c.Close(); io.Copy(c, c) }()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var workers sync.WaitGroup
			failures := make(chan error, 8)
			for i := 0; i < 8; i++ {
				workers.Add(1)
				go func(i int) {
					defer workers.Done()
					flow, e := client.Dial(ctx, echo.Addr().String())
					if e != nil {
						failures <- e
						return
					}
					defer flow.Close()
					flow.SetDeadline(time.Now().Add(4 * time.Second))
					data := bytes.Repeat([]byte{byte(i)}, 2048)
					if _, e = flow.Write(data); e != nil {
						failures <- e
						return
					}
					reply := make([]byte, len(data))
					_, e = io.ReadFull(flow, reply)
					if e == nil && !bytes.Equal(data, reply) {
						e = io.ErrUnexpectedEOF
					}
					if e != nil {
						failures <- e
					}
				}(i)
			}
			workers.Wait()
			close(failures)
			for e := range failures {
				t.Fatal(e)
			}
			conn, err := client.Dial(ctx, echo.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(4 * time.Second))
			payload := bytes.Repeat([]byte("billing"), 1024)
			if _, err = conn.Write(payload); err != nil {
				t.Fatal(err)
			}
			reply := make([]byte, len(payload))
			if _, err = io.ReadFull(conn, reply); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, reply) {
				t.Fatal("corrupt TCP payload")
			}
			udp, _ := net.ListenPacket("udp4", "127.0.0.1:0")
			defer udp.Close()
			go func() {
				b := make([]byte, 2048)
				for {
					n, a, e := udp.ReadFrom(b)
					if e != nil {
						return
					}
					udp.WriteTo(b[:n], a)
				}
			}()
			packet, err := client.DialPacket(ctx, udp.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			packet.SetDeadline(time.Now().Add(4 * time.Second))
			if _, err = packet.WriteTo([]byte("UDP-billing"), nil); err != nil {
				t.Fatal(err)
			}
			b := make([]byte, 2048)
			n, _, err := packet.ReadFrom(b)
			if err != nil {
				t.Fatal(err)
			}
			if string(b[:n]) != "UDP-billing" {
				t.Fatal("corrupt UDP payload")
			}
			traffic, err := vc.GetUserTrafficSlice(tag, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(traffic) != 1 || traffic[0].UID != 42 || traffic[0].Upload != int64(len(payload)+11+8*2048) || traffic[0].Download != int64(len(payload)+11+8*2048) {
				t.Fatalf("wrong per-account payload counters: %+v", traffic)
			}
			if more, _ := vc.GetUserTrafficSlice(tag, 0); len(more) != 0 {
				t.Fatalf("double accounting: %+v", more)
			}
			if err := vc.DelUsers([]panel.UserInfo{user}, tag, info); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(b); err == nil {
				t.Fatal("revoked TCP stream stayed open")
			}
			deadline := time.Now().Add(time.Second)
			for {
				_, _, err := packet.ReadFrom(b)
				if err != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("revoked UDP association stayed open")
				}
			}
			denied, err := client.Dial(ctx, echo.Addr().String())
			if err == nil {
				defer denied.Close()
				denied.SetDeadline(time.Now().Add(time.Second))
				_, err = denied.Read(b)
			}
			if err == nil {
				t.Fatal("revoked account opened a stream")
			}
			if err := vc.DelNode(tag); err != nil {
				t.Fatal(err)
			}
		})
	}
}
