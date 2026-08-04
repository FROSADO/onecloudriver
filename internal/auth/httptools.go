package auth

import (
	"errors"
	"net/url"
	"strings"
)

// IsOffline determines whether an error is caused by lack of network connectivity
// (DNS, connection timeout, etc.). Returns true if the error is of type
// *url.Error or does not contain "HTTP " in its message.
//
// Used during token refresh to avoid failures when the machine
// is disconnected from the internet.
func IsOffline(err error) bool {
	if err == nil {
		return false
	}
	// If the error is of type *url.Error, it's very likely a network/DNS issue
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	return !strings.Contains(err.Error(), "HTTP ")
}
