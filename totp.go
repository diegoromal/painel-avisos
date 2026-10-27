package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// TOTP RFC 6238: HMAC-SHA1, 30 s, 6 dígitos, tolerância de ±1 janela.

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

func totpCode(secret string, counter int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", v), nil
}

// verifyTOTP devolve o contador aceito (para impedir reuso) ou -1.
func verifyTOTP(secret, code string, lastUsed int64, t time.Time) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return -1
	}
	cur := t.Unix() / 30
	for _, c := range []int64{cur, cur - 1, cur + 1} {
		if c <= lastUsed {
			continue
		}
		want, err := totpCode(secret, c)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c
		}
	}
	return -1
}

func totpURI(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func qrDataURL(s string) string {
	png, err := qrcode.Encode(s, qrcode.Medium, 256)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}
