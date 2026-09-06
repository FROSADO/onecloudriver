package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frosado/onecloudriver/internal/control"
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

const syncTestAccount = "sync@outlook.com"

func syncTestInfo(mp string) *control.Info {
	return &control.Info{
		Account: control.AccountInfo{Name: syncTestAccount},
		Mount:   control.MountInfo{State: "running", Mountpoint: mp},
	}
}

func noLock() (bool, error)     { return false, nil }
func lockBusy() (bool, error)   { return true, nil }
func noService() (string, bool) { return "", false }

func TestDetectActiveMount_SocketHolder(t *testing.T) {
	sock := func(context.Context) (*control.Info, error) { return syncTestInfo("/mnt/one"), nil }
	holder := detectActiveMountWith(syncTestAccount, sock, noLock, noService)
	if holder == nil || holder.mountpoint != "/mnt/one" {
		t.Fatalf("expected socket holder /mnt/one, got %+v", holder)
	}
}

func TestDetectActiveMount_SocketOtherAccountFallsToLock(t *testing.T) {
	other := &control.Info{
		Account: control.AccountInfo{Name: "other@outlook.com"},
		Mount:   control.MountInfo{State: "running", Mountpoint: "/mnt/other"},
	}
	sock := func(context.Context) (*control.Info, error) { return other, nil }

	// Socket holder is another account → ignore; lock is free → free.
	if holder := detectActiveMountWith(syncTestAccount, sock, noLock, noService); holder != nil {
		t.Fatalf("expected no holder, got %+v", holder)
	}

	// Lock busy + service running with a mountpoint → service holder.
	svc := func() (string, bool) { return "/mnt/service", true }
	holder := detectActiveMountWith(syncTestAccount, sock, lockBusy, svc)
	if holder == nil || holder.mountpoint != "/mnt/service" {
		t.Fatalf("expected service holder /mnt/service, got %+v", holder)
	}
}

func TestDetectActiveMount_LockBusyWithoutService(t *testing.T) {
	notRunning := func(context.Context) (*control.Info, error) { return nil, control.ErrNotRunning }
	holder := detectActiveMountWith(syncTestAccount, notRunning, lockBusy, noService)
	if holder == nil || holder.mountpoint != "" {
		t.Fatalf("expected cache-only holder (no mountpoint), got %+v", holder)
	}
}

func TestDetectActiveMount_LockBusyWithService(t *testing.T) {
	notRunning := func(context.Context) (*control.Info, error) { return nil, control.ErrNotRunning }
	svc := func() (string, bool) { return "/mnt/svc", true }
	holder := detectActiveMountWith(syncTestAccount, notRunning, lockBusy, svc)
	if holder == nil || holder.mountpoint != "/mnt/svc" {
		t.Fatalf("expected service holder /mnt/svc, got %+v", holder)
	}
}

func TestDetectActiveMount_NilSeams(t *testing.T) {
	holder := detectActiveMountWith(syncTestAccount, nil, lockBusy, nil)
	if holder == nil || holder.mountpoint != "" {
		t.Fatalf("expected cache-only holder with nil seams, got %+v", holder)
	}
}

func TestDetectActiveMount_Free(t *testing.T) {
	notRunning := func(context.Context) (*control.Info, error) { return nil, control.ErrNotRunning }
	if holder := detectActiveMountWith(syncTestAccount, notRunning, noLock, noService); holder != nil {
		t.Fatalf("expected no holder when everything is free, got %+v", holder)
	}
}

func TestDetectActiveMount_ProbeErrorsAreBestEffort(t *testing.T) {
	sockErr := func(context.Context) (*control.Info, error) { return nil, errors.New("boom") }
	lockErr := func() (bool, error) { return false, errors.New("stat failed") }
	if holder := detectActiveMountWith(syncTestAccount, sockErr, lockErr, noService); holder != nil {
		t.Fatalf("expected no holder when probes error, got %+v", holder)
	}
}

func TestDetectActiveMount_AdapterOnEmptyCache(t *testing.T) {
	// Real adapter over an empty temp cache: no socket, no DB → free.
	if holder := detectActiveMount(syncTestAccount, t.TempDir()); holder != nil {
		t.Fatalf("expected no holder on an empty cache, got %+v", holder)
	}
}
