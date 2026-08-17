//go:build hbd && darwin

package cookies

import "github.com/moond4rk/hackbrowserdata/masterkey"

// nativeRetrievers returns the macOS master-key retrieval chain, deliberately
// built from DefaultRetrievers("") rather than HackBrowserData's default injector.
// The empty password drops the login-password retriever, so the only interactive
// tier is SecurityCmdRetriever — the native `security` Keychain authorization
// prompt kooky also uses. The gcore/securityd tier is a no-op unless run as root
// (and is compiled out entirely when HackBrowserData is built without
// -tags keychain_gcore).
func nativeRetrievers() masterkey.Retrievers {
	return masterkey.DefaultRetrievers("")
}
