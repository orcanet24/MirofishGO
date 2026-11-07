// Tokens de sesión: JWT HS256 firmado con SECRET_KEY (igual que Flask usa
// SecretKey). Claims mínimos: sub=user_id, role, exp.
package users

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TokenTTL dura una sesión.
const TokenTTL = 7 * 24 * time.Hour

// Claims — payload del token.
type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

// ErrInvalidToken se mapea a 401 en el middleware.
var ErrInvalidToken = errors.New("token inválido o expirado")

// NewToken firma un JWT HS256.
func NewToken(secret string, u *User) (string, error) {
	if secret == "" {
		return "", errors.New("SECRET_KEY vacía: no se pueden firmar tokens")
	}
	claims := Claims{
		Sub:  u.ID,
		Role: u.Role,
		Exp:  time.Now().Add(TokenTTL).Unix(),
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(header + "." + body))
	sig := mac.Sum(nil)
	return header + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ParseToken verifica y decodifica un token del sistema (exp incluida).
func ParseToken(secret, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrInvalidToken
	}
	if c.Exp < time.Now().Unix() {
		return nil, fmt.Errorf("%w: expirado", ErrInvalidToken)
	}
	if c.Sub == "" {
		return nil, ErrInvalidToken
	}
	return &c, nil
}
