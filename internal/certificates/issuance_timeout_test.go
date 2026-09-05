package certificates

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type deadlineTransport struct{ observed time.Duration }

func (transport *deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	if !ok {
		return nil, errors.New("missing deadline")
	}
	transport.observed = time.Until(deadline)
	<-req.Context().Done()
	return nil, req.Context().Err()
}
func TestIssuanceUsesConfiguredBudget(t *testing.T) {
	transport := &deadlineTransport{}
	manager, err := New(Config{
		Directory: t.TempDir(), Allowed: func(string) bool { return true },
		CAURL: "https://ca.test/directory", IssuanceTimeout: 20 * time.Millisecond,
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = manager.issue(ctx, "site.example.com")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error: %v", err)
	}
	if transport.observed > 20*time.Millisecond {
		t.Fatalf("budget: %v", transport.observed)
	}
}
