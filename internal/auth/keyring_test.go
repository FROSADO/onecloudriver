package auth

import "testing"

// TestKeyringKeys asserts the exact keyring key format for a given account
// name. The format is part of the offline-startup contract: tokens written
// to the OS keyring must be readable back after a process restart, and
// 'account remove' must delete exactly the same keys that were saved.
func TestKeyringKeys(t *testing.T) {
	tests := []struct {
		name       string
		wantRef    string
		wantAccess string
	}{
		{name: "user@example.com", wantRef: "onecloudriver:user@example.com", wantAccess: "onecloudriver:access:user@example.com"},
		{name: "a", wantRef: "onecloudriver:a", wantAccess: "onecloudriver:access:a"},
		{name: "", wantRef: "onecloudriver:", wantAccess: "onecloudriver:access:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRef, gotAccess := keyringKeys(tt.name)
			if gotRef != tt.wantRef {
				t.Errorf("keyringKeys(%q) refresh key = %q, want %q", tt.name, gotRef, tt.wantRef)
			}
			if gotAccess != tt.wantAccess {
				t.Errorf("keyringKeys(%q) access key = %q, want %q", tt.name, gotAccess, tt.wantAccess)
			}
		})
	}
}
