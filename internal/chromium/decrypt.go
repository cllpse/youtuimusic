package chromium

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/cllpse/youtuimusic/internal/jar"
)

// Chromium's cookie encryption, which is the same everywhere it is not
// Windows: a key stretched from a password with PBKDF2, then AES-CBC with a
// fixed initialisation vector. None of these constants are secret — they are
// in Chromium's source — and the security comes from where the password is
// kept, not from them.
const (
	keySalt = "saltysalt"
	keyLen  = 16
	// v10 values are encrypted with a hardcoded password: it is what
	// Chromium falls back to when no keyring is available, so the value is
	// obfuscated rather than protected.
	fallbackPassword = "peanuts"
)

// iv is sixteen spaces.
var iv = []byte("                ")

// ErrEncrypted means a cookie could not be decrypted.
var ErrEncrypted = errors.New("chromium: cookie could not be decrypted")

// deriveKey stretches a storage password into an AES key. Linux uses a
// single iteration and macOS 1003 — a difference in Chromium, not a choice
// available to us.
func deriveKey(password []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha1.New, string(password), []byte(keySalt), iterations, keyLen)
}

// decrypt turns a stored encrypted_value into the cookie's text, trying each
// key the set holds for the value's prefix.
//
// A key is taken only when its result unpads and reads as a cookie value. The
// padding check alone lets a wrong key through about once in 256 cookies, and
// what that produces is noise that would go out as a session cookie.
func decrypt(value []byte, host string, keys keySet) (string, error) {
	if len(value) < 3 {
		return "", fmt.Errorf("%w: value too short", ErrEncrypted)
	}
	version := string(value[:3])
	candidates, ok := keys[version]
	if !ok {
		return "", fmt.Errorf("%w: unsupported format %q", ErrEncrypted, version)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w: no key for %q values", ErrEncrypted, version)
	}
	err := fmt.Errorf("%w: no key fits", ErrEncrypted)
	for _, key := range candidates {
		plain, e := decryptWith(value[3:], host, key)
		if e != nil {
			err = e
			continue
		}
		if jar.Valid(plain) {
			return plain, nil
		}
		err = fmt.Errorf("%w: not a cookie value (probably the wrong key)", ErrEncrypted)
	}
	return "", err
}

// decryptWith is one key's attempt.
//
// host is needed because newer Chromium prepends a SHA-256 of the cookie's
// domain to the plaintext, binding the value to the host it was set for.
// Rather than test the browser's version, this checks for the hash and
// strips it when it is there, which is self-verifying and survives whatever
// the next version does.
func decryptWith(body []byte, host string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(body) == 0 || len(body)%block.BlockSize() != 0 {
		return "", fmt.Errorf("%w: ciphertext is not a whole number of blocks", ErrEncrypted)
	}
	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, body)

	plain, err = unpad(plain, block.BlockSize())
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256([]byte(host)); len(plain) >= len(sum) &&
		string(plain[:len(sum)]) == string(sum[:]) {
		plain = plain[len(sum):]
	}
	return string(plain), nil
}

// unpad removes PKCS#7 padding. A wrong key usually shows up here, as a
// final byte that is not a plausible padding length.
func unpad(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: empty plaintext", ErrEncrypted)
	}
	n := int(b[len(b)-1])
	if n == 0 || n > blockSize || n > len(b) {
		return nil, fmt.Errorf("%w: bad padding (probably the wrong key)", ErrEncrypted)
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, fmt.Errorf("%w: inconsistent padding (probably the wrong key)", ErrEncrypted)
		}
	}
	return b[:len(b)-n], nil
}
