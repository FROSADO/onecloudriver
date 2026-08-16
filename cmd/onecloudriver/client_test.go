package main

import (
	"context"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/spf13/cobra"
)

// TestGetClient_ReturnsClientFromContext verifies that getClient returns the
// exact instance stored by contextWithClient (the shared client created once
// in PersistentPreRun).
func TestGetClient_ReturnsClientFromContext(t *testing.T) {
	want := graph.NewClient()
	cmd := &cobra.Command{}
	cmd.SetContext(contextWithClient(context.Background(), want))

	got := getClient(cmd)
	if got != want {
		t.Fatalf("getClient returned %p, want %p (same shared instance)", got, want)
	}
}

// TestGetClient_FallsBackWhenNotInContext verifies getClient returns a usable
// client when the context has none (e.g. a RunE invoked without going through
// rootCmd's PersistentPreRun, as some tests do).
func TestGetClient_FallsBackWhenNotInContext(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	if got := getClient(cmd); got == nil {
		t.Fatal("getClient should return a usable client when none is in the context")
	}
}

// TestGetClient_IgnoresNonClientContextValue verifies a non-*graph.Client value
// under the key is ignored (falls back) instead of panicking.
func TestGetClient_IgnoresNonClientContextValue(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), clientKey{}, "not-a-client"))

	if got := getClient(cmd); got == nil {
		t.Fatal("getClient should fall back to a fresh client for a non-*graph.Client value")
	}
}
