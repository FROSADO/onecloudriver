# API: internal/types

> Auto-generated with `go doc -all`. Date: 2026-08-14 00:40:10

```
package types // import "github.com/frosado/onecloudriver/internal/types"

Package types defines the shared interfaces between internal packages of the
project, avoiding circular dependencies.

TYPES

type TokenProvider interface {
	// GetAccessToken obtains a valid access token for Microsoft Graph.
	//
	// If the current token is expired, the implementation must refresh it
	// automatically before returning it.
	//
	// The context allows canceling long-running network operations (such as refresh).
	GetAccessToken(ctx context.Context) (string, error)
}
    TokenProvider is an interface for anything that can provide a Microsoft
    Graph access token.

    This interface allows the graph package to work with tokens without directly
    depending on the auth package, avoiding import cycles.

    Example implementation:

        type Account struct {
            accessToken string
        }

        func (a *Account) GetAccessToken(ctx context.Context) (string, error) {
            if tokenIsExpired() {
                return refreshToken(ctx)
            }
            return a.accessToken, nil
        }

```
