package node

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/common/file"
)

func (c *Controller) renewCertTask(_ context.Context) error {
	l, err := NewLego(c.info.Common.CertInfo)
	if err != nil {
		log.WithField("tag", c.tag).Info("new lego error: ", err)
		return nil
	}
	err = l.RenewCert(isFCNCertificateNode(c.info.Type))
	if err != nil {
		log.WithField("tag", c.tag).Info("renew cert error: ", err)
		return nil
	}
	return nil
}

func (c *Controller) requestCert() error {
	cert := c.info.Common.CertInfo
	switch cert.CertMode {
	case "none", "":
	case "file":
		if cert.CertFile == "" || cert.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
	case "dns", "http":
		if cert.CertFile == "" || cert.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
		if isFCNCertificateNode(c.info.Type) {
			if cert.CertDomain == "" {
				return fmt.Errorf("FCN automatic certificates require a server name")
			}
			if reusableFCNAutomaticCertificate(cert, nil) {
				return nil
			}
		} else if file.IsExist(cert.CertFile) && file.IsExist(cert.KeyFile) {
			return nil
		}
		l, err := NewLego(cert)
		if err != nil {
			return fmt.Errorf("create lego object error: %s", err)
		}
		err = l.CreateCert()
		if err != nil {
			return fmt.Errorf("create lego cert error: %s", err)
		}
	case "self":
		if cert.CertFile == "" || cert.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
		if file.IsExist(cert.CertFile) && file.IsExist(cert.KeyFile) {
			return nil
		}
		err := generateSelfSslCertificate(
			cert.CertDomain,
			cert.CertFile,
			cert.KeyFile)
		if err != nil {
			return fmt.Errorf("generate self cert error: %s", err)
		}
	case "remote":
		if cert.CertFile == "" || cert.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
		if file.IsExist(cert.CertFile) && file.IsExist(cert.KeyFile) {
			return nil
		}
		if cert.TlsCert == "" || cert.TlsKey == "" {
			return fmt.Errorf("tls cert or tls key not exist")
		}
		if err := os.WriteFile(cert.CertFile, []byte(cert.TlsCert), 0644); err != nil {
			return fmt.Errorf("write remote cert error: %s", err)
		}
		if err := os.WriteFile(cert.KeyFile, []byte(cert.TlsKey), 0600); err != nil {
			return fmt.Errorf("write remote key error: %s", err)
		}

	default:
		return fmt.Errorf("unsupported certmode: %s", cert.CertMode)
	}
	return nil
}

func isFCNCertificateNode(nodeType string) bool {
	return nodeType == "fcn-v1" || nodeType == "fcn-v2" || nodeType == "fcn-v3"
}

func reusableFCNAutomaticCertificate(cert *panel.CertInfo, roots *x509.CertPool) bool {
	pair, err := tls.LoadX509KeyPair(cert.CertFile, cert.KeyFile)
	if err != nil || cert.CertDomain == "" {
		return false
	}
	leaf := pair.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return false
		}
	}
	chain := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		intermediate, err := x509.ParseCertificate(der)
		if err != nil {
			return false
		}
		chain.AddCert(intermediate)
	}
	// A previous self-signed or expired certificate can share these paths.
	// Require a valid chain for the current HTTP-01 / DNS-01 identity.
	_, err = leaf.Verify(x509.VerifyOptions{
		DNSName: cert.CertDomain, Roots: roots, Intermediates: chain,
	})
	return err == nil
}

func generateSelfSslCertificate(domain, certPath, keyPath string) error {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		Version:      3,
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			CommonName: domain,
		},
		DNSNames:              []string{domain},
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(30, 0, 0),
	}
	cert, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(certPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	err = pem.Encode(f, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})
	if err != nil {
		return err
	}
	f, err = os.OpenFile(keyPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	err = pem.Encode(f, &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		return err
	}
	return nil
}
