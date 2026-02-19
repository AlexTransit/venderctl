package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func checkTelegramAuth(values url.Values, botToken string) bool {
	hash := values.Get("hash")
	if hash == "" {
		return false
	}

	dataCheck := []string{}
	for key, val := range values {
		if key == "hash" {
			continue
		}

		v := val[0]

		if key == "photo_url" {
			decoded, err := url.QueryUnescape(v)
			if err == nil {
				v = decoded
			}
		}

		dataCheck = append(dataCheck, fmt.Sprintf("%s=%s", key, v))
	}

	sort.Strings(dataCheck)
	dataString := strings.Join(dataCheck, "\n")

	secretKey := sha256.Sum256([]byte(botToken))

	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write([]byte(dataString))
	expectedHash := hex.EncodeToString(mac.Sum(nil))

	return expectedHash == hash
}
