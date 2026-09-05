package hosting

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

type deadlineRecorder struct {
	net.Conn
	deadlines []time.Time
}

func (c *deadlineRecorder) SetDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	return nil
}

func TestCertificateDeadlineBudgetAndRestoration(t *testing.T) {
	for _, failed := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			conn := &deadlineRecorder{}
			issuanceError := errors.New("issuance failed")
			cfg := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				time.Sleep(20 * time.Second)
				if failed {
					return nil, issuanceError
				}
				return &tls.Certificate{}, nil
			}}
			allowCertificateIssuance(cfg, 10*time.Second, 45*time.Second)
			_, err := cfg.GetCertificate(&tls.ClientHelloInfo{Conn: conn})
			if failed != errors.Is(err, issuanceError) {
				t.Fatalf("issuance error: %v", err)
			}
			if len(conn.deadlines) != 2 {
				t.Fatalf("deadlines: %v", conn.deadlines)
			}
			if !conn.deadlines[0].Equal(start.Add(55*time.Second)) || !conn.deadlines[1].Equal(start.Add(30*time.Second)) {
				t.Fatalf("deadlines: %v", conn.deadlines)
			}
		})
	}
}
