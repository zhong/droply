//go:build integration

package hosting_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/zhong/droply/internal/hosting"
)

func TestListenersServeTLSAndShutdown(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(mustParse(t, der))
	svc, err := hosting.Start(hosting.Config{HTTPAddr: "127.0.0.1:0", HTTPSAddr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "droply") }), TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown(context.Background()) })
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost"}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, address := range []string{"http://" + svc.HTTPAddress(), "https://" + svc.HTTPSAddress()} {
		resp, err := client.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "droply" {
			t.Fatalf("body %q err %v", body, err)
		}
	}
	if err := svc.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get("http://" + svc.HTTPAddress()); err == nil {
		t.Fatal("listener still available after shutdown")
	}
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestPortConflictDoesNotStartPartialService(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	svc, err := hosting.Start(hosting.Config{HTTPAddr: "127.0.0.1:0", HTTPSAddr: busy.Addr().String(), Handler: http.NotFoundHandler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}})
	if err == nil {
		svc.Shutdown(t.Context())
		t.Fatal("expected busy-port error")
	}
}
