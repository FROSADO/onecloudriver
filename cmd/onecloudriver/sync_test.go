package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
)

// TestSyncCmd_AppliesRemoteChanges verifies the sync command end-to-end
// (issue #73): it resolves the account, runs a single delta poll against a
// mocked Graph delta endpoint and reports the number of changes applied. A
// temp HOME makes DefaultMountConfig create the account's cache (BoltDB +
// content) in the test sandbox instead of the real user cache.
func TestSyncCmd_AppliesRemoteChanges(t *testing.T) {
	accountName := "test@example.com"
	setupManager(t, accountName)
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			DeltaLink string            `json:"@odata.deltaLink,omitempty"`
			Values    []graph.DeltaItem `json:"value,omitempty"`
		}{
			DeltaLink: "http://" + r.Host + "/delta/final",
			Values: []graph.DeltaItem{
				{DriveItem: graph.DriveItem{ID: "remote1", Name: "remote.txt", Size: 10, Parent: &graph.DriveItemParent{ID: "root"}}},
			},
		})
	}))
	defer server.Close()

	// setupManager() has already set up rootCmd.PersistentPreRun which injects the manager.
	// Call it manually to populate the context for our test command with a background context.
	cmd := syncCmd
	cmd.SetContext(context.Background())
	rootCmd.PersistentPreRun(cmd, nil)

	// Inject the mocked client (the manager was already injected by PersistentPreRun).
	ctx := contextWithClient(cmd.Context(), &graph.Client{BaseURL: server.URL, HTTPClient: server.Client()})
	cmd.SetContext(ctx)

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("sync RunE error: %v", err)
	}

	if !strings.Contains(out.String(), "1 change applied") {
		t.Errorf("expected summary '1 change applied', got: %q", out.String())
	}
}

// TestSyncCmd_HasAccountFlag verifies the sync command exposes the standard
// --account flag (same preamble as list/upload).
func TestSyncCmd_HasAccountFlag(t *testing.T) {
	accountFlag := syncCmd.Flags().Lookup("account")
	if accountFlag == nil {
		t.Fatal("sync missing --account flag")
	}
	if accountFlag.Shorthand != "a" {
		t.Errorf("sync --account shorthand: expected 'a', got %q", accountFlag.Shorthand)
	}
}
