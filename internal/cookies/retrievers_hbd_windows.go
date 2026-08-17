//go:build hbd && windows

package cookies

import "github.com/moond4rk/hackbrowserdata/masterkey"

// nativeRetrievers returns the Windows master-key retrieval chain with DPAPI only
// (v10), deliberately omitting the v20 App-Bound-Encryption retriever. ABE
// decryption relies on reflective payload injection into a browser process, which
// trips antivirus/EDR; skipping it keeps the read benign. Chrome 127+ ABE cookies
// therefore don't decrypt — the user falls back to GH_SESSION_TOKEN, unchanged
// from the kooky provider's behavior on the same machines.
func nativeRetrievers() masterkey.Retrievers {
	return masterkey.Retrievers{V10: &masterkey.DPAPIRetriever{}}
}
