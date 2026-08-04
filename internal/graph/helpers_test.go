package graph

import "context"

// mockTokenProvider es un mock de types.TokenProvider para tests
type mockTokenProvider struct {
	token string
	err   error
}

func (m *mockTokenProvider) GetAccessToken(ctx context.Context) (string, error) {
	return m.token, m.err
}
