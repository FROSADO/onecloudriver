package auth

// keyringService is the service name used for every OS keyring entry.
// The key format is part of the offline-startup contract (a process
// restart reads the tokens back with exactly these keys), so all
// construction sites must share a single source of truth.
const keyringService = "onecloudriver"

// keyringKeys returns the keyring keys used to store the refresh and
// access tokens of the given account:
//
//	refreshKey: onecloudriver:<name>
//	accessKey:  onecloudriver:access:<name>
//
// The access token lives under a separate key (it is never persisted on
// disk, security S1) so that a process restart without network can start
// in offline mode while the token is still valid.
func keyringKeys(name string) (refreshKey, accessKey string) {
	refreshKey = keyringService + ":" + name
	accessKey = keyringService + ":access:" + name
	return refreshKey, accessKey
}
