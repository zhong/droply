//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func availableAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestStandaloneRestartPreservesUsersAndSigningKey(t *testing.T) {
	data := t.TempDir()
	var token string
	var firstKey []byte
	for round := range 2 {
		addr := availableAddress(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- run(ctx, []string{"--addr", addr, "--data-dir", data, "--domain", "example.test"}) }()
		client := &http.Client{Timeout: time.Second}
		request := func(method, path string, body []byte) *http.Response {
			t.Helper()
			req, err := http.NewRequest(method, "http://"+addr+path, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "api.example.test"
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			return resp
		}
		waitForListener(t, addr, done)
		if round == 0 {
			resp := request("POST", "/auth/register", []byte(`{"email":"owner@example.test","password":"test-password-123"}`))
			var result struct {
				Token string `json:"api_token"`
			}
			json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if resp.StatusCode != 201 || result.Token == "" {
				t.Fatalf("registration: %d", resp.StatusCode)
			}
			token = result.Token
			firstKey, _ = os.ReadFile(filepath.Join(data, "hmac.key"))
		} else {
			resp := request("GET", "/subdomains", nil)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("old token failed: %d", resp.StatusCode)
			}
			key, _ := os.ReadFile(filepath.Join(data, "hmac.key"))
			if !bytes.Equal(key, firstKey) {
				t.Fatal("HMAC key changed")
			}
		}
		client.CloseIdleConnections()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("shutdown did not finish")
		}
	}
}

func waitForListener(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		select {
		case err := <-done:
			t.Fatalf("server exited: %v", err)
		case <-deadline.C:
			t.Fatal("server did not start")
		case <-ticker.C:
		}
	}
}

func TestManualTLSStartupAndAPI(t *testing.T) {
	data := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(42), DNSNames: []string{"api.example.test", "*.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(data, "cert.pem"), filepath.Join(data, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600)
	roots := x509.NewCertPool()
	cert, _ := x509.ParseCertificate(der)
	roots.AddCert(cert)
	httpAddr, tlsAddr := availableAddress(t), availableAddress(t)
	args := []string{"--addr", httpAddr, "--https-addr", tlsAddr, "--data-dir", data, "--domain", "example.test", "--tls-mode", "manual", "--tls-cert", certFile, "--tls-key", keyFile}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, args) }()
	waitForListener(t, tlsAddr, done)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "api.example.test"}}, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for _, scheme := range []string{"http", "https"} {
		address := httpAddr
		if scheme == "https" {
			address = tlsAddr
		}
		req, _ := http.NewRequest("GET", scheme+"://"+address+"/subdomains", nil)
		req.Host = "api.example.test"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		expected := 308
		if scheme == "https" {
			expected = 401
		}
		if resp.StatusCode != expected {
			t.Fatalf("%s: %d", scheme, resp.StatusCode)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Invalid key pair fails before serving or silently falling back to HTTP.
	os.WriteFile(keyFile, []byte("not a key"), 0600)
	if err := run(t.Context(), args); err == nil {
		t.Fatal("invalid certificate accepted")
	}
}

func TestInvalidHMACKeyDoesNotRotate(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "hmac.key")
	os.WriteFile(path, []byte("broken"), 0600)
	if _, err := loadOrGenerateHMACKey("", data); err == nil {
		t.Fatal("invalid key accepted")
	}
	contents, _ := os.ReadFile(path)
	if string(contents) != "broken" {
		t.Fatal("key overwritten")
	}
}
