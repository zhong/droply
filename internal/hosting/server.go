// Package hosting owns public listeners and drains requests before application
// resources are closed. All ports are acquired before any request is served.
package hosting

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

type Config struct {
	HTTPAddr    string
	HTTPSAddr   string
	Handler     http.Handler
	HTTPHandler http.Handler
	TLSConfig   *tls.Config
}

type Service struct {
	servers      []*http.Server
	httpAddress  string
	httpsAddress string
	errors       chan error
	wg           sync.WaitGroup
}

func Start(cfg Config) (*Service, error) {
	if cfg.Handler == nil {
		return nil, errors.New("HTTP handler is required")
	}
	if cfg.HTTPAddr == "" && cfg.HTTPSAddr == "" {
		return nil, errors.New("at least one listener is required")
	}
	if cfg.HTTPSAddr != "" && cfg.TLSConfig == nil {
		return nil, errors.New("HTTPS requires TLS configuration")
	}
	svc := &Service{errors: make(chan error, 2)}
	var listeners []net.Listener
	for _, address := range []string{cfg.HTTPAddr, cfg.HTTPSAddr} {
		if address == "" {
			listeners = append(listeners, nil)
			continue
		}
		ln, err := net.Listen("tcp", address)
		if err != nil {
			for _, opened := range listeners {
				if opened != nil {
					opened.Close()
				}
			}
			return nil, fmt.Errorf("listen on %s: %w", address, err)
		}
		listeners = append(listeners, ln)
	}
	for i, ln := range listeners {
		if ln == nil {
			continue
		}
		handler := cfg.Handler
		if i == 0 && cfg.HTTPHandler != nil {
			handler = cfg.HTTPHandler
		}
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: time.Minute, MaxHeaderBytes: 1 << 20}
		if i == 0 {
			svc.httpAddress = ln.Addr().String()
		} else {
			svc.httpsAddress = ln.Addr().String()
			srv.TLSConfig = cfg.TLSConfig.Clone()
			allowCertificateIssuance(srv.TLSConfig, srv.ReadHeaderTimeout)
		}
		svc.servers = append(svc.servers, srv)
		svc.wg.Go(func() {
			var err error
			if i == 1 {
				err = srv.ServeTLS(ln, "", "")
			} else {
				err = srv.Serve(ln)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				svc.errors <- err
			}
		})
	}
	return svc, nil
}

// net/http applies ReadHeaderTimeout to the entire TLS handshake using socket
// deadlines. ACME can legitimately take longer after ClientHello has arrived.
// Give only certificate selection extra time, then restore the short deadline
// for the peer to finish its handshake. ServeTLS retains native HTTP/2 support.
func allowCertificateIssuance(cfg *tls.Config, handshakeTimeout time.Duration) {
	getCertificate := cfg.GetCertificate
	if getCertificate == nil {
		return
	}
	cfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if err := hello.Conn.SetDeadline(time.Now().Add(5*time.Minute + handshakeTimeout)); err != nil {
			return nil, err
		}
		cert, err := getCertificate(hello)
		deadlineErr := hello.Conn.SetDeadline(time.Now().Add(handshakeTimeout))
		if err != nil {
			return nil, err
		}
		if deadlineErr != nil {
			return nil, deadlineErr
		}
		return cert, nil
	}
}

func (s *Service) HTTPAddress() string  { return s.httpAddress }
func (s *Service) HTTPSAddress() string { return s.httpsAddress }
func (s *Service) Errors() <-chan error { return s.errors }

// Shutdown drains both listeners concurrently. If the deadline expires, force
// remaining connections closed before returning to the resource owner.
func (s *Service) Shutdown(ctx context.Context) error {
	errs := make(chan error, len(s.servers))
	var wg sync.WaitGroup
	for _, srv := range s.servers {
		wg.Go(func() {
			if err := srv.Shutdown(ctx); err != nil {
				srv.Close()
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	s.wg.Wait()
	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}
