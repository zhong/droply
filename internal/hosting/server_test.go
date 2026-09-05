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
	"net/http/httptest"
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

func TestCertificateIssuanceMayOutlastHeaderTimeout(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	defer fixture.Close()
	pair := fixture.TLS.Certificates[0]
	leaf := fixture.Certificate()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	svc, err := hosting.Start(hosting.Config{CertificateTimeout: 30 * time.Second, HTTPSAddr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "issued") }), TLSConfig: &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		select {
		case <-time.After(11 * time.Second):
			return &pair, nil
		case <-hello.Context().Done():
			return nil, hello.Context().Err()
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		svc.Shutdown(ctx)
	})
	// A peer that never sends ClientHello must still hit the original ten-second limit.
	idle, err := net.Dial("tcp", svc.HTTPSAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	idleDone := make(chan error, 1)
	go func() {
		idle.SetReadDeadline(time.Now().Add(14 * time.Second))
		var b [1]byte
		_, err := idle.Read(b[:])
		idleDone <- err
	}()
	client := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: leaf.DNSNames[0]}}, Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	res, err := client.Get("https://" + svc.HTTPSAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.ProtoMajor != 2 {
		t.Fatalf("native HTTP/2 lost: %s", res.Proto)
	}
	if data, err := io.ReadAll(res.Body); err != nil || string(data) != "issued" {
		t.Fatalf("response %q: %v", data, err)
	}
	if err := <-idleDone; err == nil {
		t.Fatal("idle TLS peer was not closed")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("initial handshake deadline was extended for an idle peer")
	}
}
