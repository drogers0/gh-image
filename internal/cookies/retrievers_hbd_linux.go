//go:build hbd && linux

package cookies

import "github.com/moond4rk/hackbrowserdata/masterkey"

// nativeRetrievers returns the Linux master-key retrieval chain: the deterministic
// PBKDF2("peanuts") v10 key plus the D-Bus Secret Service (GNOME Keyring / KDE
// Wallet) v11 retriever. Both are legitimate keyring reads — the same posture as
// kooky's libsecret path, with no offensive code involved.
func nativeRetrievers() masterkey.Retrievers {
	return masterkey.DefaultRetrievers()
}
