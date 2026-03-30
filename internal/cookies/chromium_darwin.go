//go:build darwin

package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

type browserDef struct {
	name        string
	storageKey  string // macOS Keychain service name
	cookiePaths []string
}

var browsers = []browserDef{
	{
		name:       "Arc",
		storageKey: "Arc Safe Storage",
		cookiePaths: []string{
			"Arc/User Data/Default/Cookies",
		},
	},
	{
		name:       "Brave",
		storageKey: "Brave Safe Storage",
		cookiePaths: []string{
			"BraveSoftware/Brave-Browser/Default/Cookies",
		},
	},
	{
		name:       "Chrome",
		storageKey: "Chrome Safe Storage",
		cookiePaths: []string{
			"Google/Chrome/Default/Cookies",
		},
	},
	{
		name:       "Chromium",
		storageKey: "Chromium Safe Storage",
		cookiePaths: []string{
			"Chromium/Default/Cookies",
		},
	},
	{
		name:       "Edge",
		storageKey: "Microsoft Edge Safe Storage",
		cookiePaths: []string{
			"Microsoft Edge/Default/Cookies",
		},
	},
}

// directReadGitHubSession tries each browser's cookie DB directly using
// sqlite3 CLI and macOS Keychain for decryption. This bypasses kooky
// and supports browsers like Arc that kooky doesn't handle.
func directReadGitHubSession() (*http.Cookie, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("getting config dir: %w", err)
	}

	var errs []string
	for _, b := range browsers {
		for _, rel := range b.cookiePaths {
			dbPath := filepath.Join(configDir, rel)
			if _, err := os.Stat(dbPath); err != nil {
				continue
			}
			cookie, err := readCookie(dbPath, b.storageKey)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", b.name, err))
				continue
			}
			return cookie, nil
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("direct cookie read failed: %s", strings.Join(errs, "; "))
	}
	return nil, fmt.Errorf("no supported browser cookie DB found")
}

func readCookie(dbPath, storageKey string) (*http.Cookie, error) {
	// Copy DB + WAL files to temp dir to avoid lock contention with the browser.
	tmpDir, err := os.MkdirTemp("", "gh-image-cookies-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := dbPath + suffix
		if data, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(tmpDir, "Cookies"+suffix), data, 0600)
		}
	}

	tmpDB := filepath.Join(tmpDir, "Cookies")

	// Query encrypted cookie value.
	query := `SELECT hex(encrypted_value) FROM cookies WHERE host_key LIKE '%github.com%' AND name='user_session' ORDER BY last_access_utc DESC LIMIT 1;`
	out, err := exec.Command("sqlite3", tmpDB, query).Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite3: %w", err)
	}
	hexVal := strings.TrimSpace(string(out))
	if hexVal == "" {
		return nil, fmt.Errorf("user_session cookie not found")
	}

	encVal, err := hex.DecodeString(hexVal)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}

	// Get encryption key from macOS Keychain.
	keyOut, err := exec.Command("security", "find-generic-password", "-s", storageKey, "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain (%s): %w", storageKey, err)
	}
	rawKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyOut)))
	if err != nil {
		return nil, fmt.Errorf("base64 key: %w", err)
	}

	// PBKDF2 key derivation (Chromium standard: 1003 iterations, 16-byte key).
	derived := pbkdf2.Key(rawKey, []byte("saltysalt"), 1003, 16, sha1.New)

	value, err := decryptCookie(encVal, derived)
	if err != nil {
		return nil, err
	}

	return &http.Cookie{
		Name:     "user_session",
		Value:    value,
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}, nil
}

func decryptCookie(enc, key []byte) (string, error) {
	// Chromium on macOS prefixes encrypted values with "v10".
	if len(enc) < 3 || string(enc[:3]) != "v10" {
		return "", fmt.Errorf("unexpected encryption prefix: %x", enc[:min(3, len(enc))])
	}
	ciphertext := enc[3:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid ciphertext length %d", len(ciphertext))
	}

	// IV: 16 bytes of 0x20 (space).
	iv := []byte("                ")
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	// Strip PKCS#7 padding.
	if pad := int(plaintext[len(plaintext)-1]); pad > 0 && pad <= aes.BlockSize {
		plaintext = plaintext[:len(plaintext)-pad]
	}

	return string(plaintext), nil
}
