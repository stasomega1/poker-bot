package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInitDataValidatorValidate(t *testing.T) {
	botToken := "123456:test-token"
	authDate := time.Now().Add(-time.Minute).Unix()
	values := url.Values{}
	values.Set("auth_date", "0")
	values.Set("query_id", "query-1")
	values.Set("user", `{"id":42,"first_name":"Stas","username":"stas"}`)
	values.Set("auth_date", formatUnix(authDate))

	raw := signedInitData(t, botToken, values)
	validator := NewInitDataValidator(botToken, 123456789, time.Hour)
	validator.now = func() time.Time {
		return time.Unix(authDate, 0).Add(time.Minute)
	}

	auth, err := validator.Validate(raw)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if auth.User.ID != 42 {
		t.Fatalf("expected user id 42, got %d", auth.User.ID)
	}
	if DisplayUserName(auth.User) != "@stas" {
		t.Fatalf("unexpected display name: %s", DisplayUserName(auth.User))
	}
}

func TestInitDataValidatorRejectsInvalidHash(t *testing.T) {
	values := url.Values{}
	values.Set("auth_date", formatUnix(time.Now().Unix()))
	values.Set("user", `{"id":42}`)
	values.Set("hash", "bad")

	_, err := NewInitDataValidator("123456:test-token", 123456789, time.Hour).Validate(values.Encode())
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func signedInitData(t *testing.T, botToken string, values url.Values) string {
	t.Helper()

	pairs := make([]string, 0, len(values))
	for key, items := range values {
		if key == "hash" || len(items) == 0 {
			continue
		}
		pairs = append(pairs, key+"="+items[0])
	}
	sort.Strings(pairs)

	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	secretMAC.Write([]byte(botToken))
	secret := secretMAC.Sum(nil)

	dataMAC := hmac.New(sha256.New, secret)
	dataMAC.Write([]byte(strings.Join(pairs, "\n")))
	values.Set("hash", hex.EncodeToString(dataMAC.Sum(nil)))
	return values.Encode()
}

func formatUnix(value int64) string {
	return strconv.FormatInt(value, 10)
}
