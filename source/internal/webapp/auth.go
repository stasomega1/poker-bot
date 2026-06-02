package webapp

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TelegramUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type AuthData struct {
	User     TelegramUser
	AuthAt   time.Time
	InitData string
}

type InitDataValidator struct {
	botToken string
	botID    int64
	maxAge   time.Duration
	now      func() time.Time
}

const telegramMiniAppProductionPublicKeyHex = "e7bf03a2fa4602af4580703d88dda5bb59f32ed8b02a56c187fe7d34caed242d"

func NewInitDataValidator(botToken string, botID int64, maxAge time.Duration) *InitDataValidator {
	return &InitDataValidator{
		botToken: botToken,
		botID:    botID,
		maxAge:   maxAge,
		now:      time.Now,
	}
}

func (v *InitDataValidator) Validate(raw string) (AuthData, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AuthData{}, fmt.Errorf("missing Telegram init data")
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return AuthData{}, fmt.Errorf("parse Telegram init data: %w", err)
	}

	gotHash := values.Get("hash")
	if gotHash == "" {
		return AuthData{}, fmt.Errorf("missing Telegram init data hash")
	}

	pairs := make([]string, 0, len(values))
	for key, items := range values {
		if key == "hash" || key == "signature" {
			continue
		}
		if len(items) == 0 {
			continue
		}
		pairs = append(pairs, key+"="+items[0])
	}
	sort.Strings(pairs)
	dataCheckString := strings.Join(pairs, "\n")

	secretMAC := hmac.New(sha256.New, []byte("WebAppData"))
	secretMAC.Write([]byte(v.botToken))
	secret := secretMAC.Sum(nil)

	dataMAC := hmac.New(sha256.New, secret)
	dataMAC.Write([]byte(dataCheckString))
	expectedHash := hex.EncodeToString(dataMAC.Sum(nil))
	if !hmac.Equal([]byte(expectedHash), []byte(gotHash)) {
		if err := v.validateSignature(values, dataCheckString); err != nil {
			return AuthData{}, fmt.Errorf("invalid Telegram init data hash")
		}
	}

	authUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || authUnix <= 0 {
		return AuthData{}, fmt.Errorf("invalid Telegram auth date")
	}
	authAt := time.Unix(authUnix, 0)
	if v.maxAge > 0 && v.now().After(authAt.Add(v.maxAge)) {
		return AuthData{}, fmt.Errorf("Telegram init data is expired")
	}

	var user TelegramUser
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil {
		return AuthData{}, fmt.Errorf("parse Telegram user: %w", err)
	}
	if user.ID == 0 {
		return AuthData{}, fmt.Errorf("Telegram user id is missing")
	}

	return AuthData{
		User:     user,
		AuthAt:   authAt,
		InitData: raw,
	}, nil
}

func (v *InitDataValidator) validateSignature(values url.Values, dataCheckString string) error {
	signature := strings.TrimSpace(values.Get("signature"))
	if signature == "" || v.botID == 0 {
		return fmt.Errorf("signature validation unavailable")
	}

	publicKey, err := hex.DecodeString(telegramMiniAppProductionPublicKeyHex)
	if err != nil {
		return err
	}

	signatureBytes, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		signatureBytes, err = base64.URLEncoding.DecodeString(signature)
		if err != nil {
			return err
		}
	}

	message := strconv.FormatInt(v.botID, 10) + ":WebAppData\n" + dataCheckString
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(message), signatureBytes) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

func DisplayUserName(user TelegramUser) string {
	if username := strings.TrimSpace(user.Username); username != "" {
		return "@" + username
	}
	name := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " "))
	if name != "" {
		return name
	}
	return strconv.FormatInt(user.ID, 10)
}
