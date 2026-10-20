package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"time"
)

// The high-entropy administrator secret bootstraps a shared cluster identity.
// Domain separation prevents reuse as an SDK key. Ordinary service credentials
// cannot authenticate on either internal replication listener.
func clusterTLS(options Options) (*tls.Config, error) {
	seed := sha256.Sum256([]byte("keel cluster transport v1\x00" + options.Cluster.Name + "\x00" + options.Credentials.Admin))
	key := ed25519.NewKeyFromSeed(seed[:])
	name := "keel-" + hex.EncodeToString(seed[:16])
	cert := &x509.Certificate{SerialNumber: new(big.Int).SetBytes(seed[:16]), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return &tls.Config{MinVersion: tls.VersionTLS13, ServerName: name, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}}, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}, nil
}
