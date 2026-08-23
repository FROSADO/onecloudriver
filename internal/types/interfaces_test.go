package types

import (
	"context"
	"testing"
)

type contractTokenProvider struct {
	token       string
	seenContext context.Context
}

type tokenProviderContextKey struct{}

var _ TokenProvider = (*contractTokenProvider)(nil)

func (p *contractTokenProvider) GetAccessToken(ctx context.Context) (string, error) {
	p.seenContext = ctx
	return p.token, nil
}

func TestTokenProviderContract(t *testing.T) {
	ctx := context.WithValue(context.Background(), tokenProviderContextKey{}, "value")
	provider := &contractTokenProvider{token: "access-token"}
	token, err := provider.GetAccessToken(ctx)
	if err != nil {
		t.Fatalf("GetAccessToken: %v", err)
	}
	if token != "access-token" {
		t.Errorf("token = %q, want access-token", token)
	}
	if provider.seenContext != ctx {
		t.Fatal("GetAccessToken did not receive the caller context")
	}
}
