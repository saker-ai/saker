package server

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// checkCredentials verifies login credentials against admin and regular users.
// Returns (matched, role) where role is "admin" or "user".
func (a *AuthManager) checkCredentials(username, password string) (bool, string) {
	if a.cfg == nil || a.cfg.Password == "" {
		return false, ""
	}

	// Check admin account first.
	adminUser := a.cfg.Username
	if adminUser == "" {
		adminUser = "admin"
	}
	if username == adminUser {
		if err := bcrypt.CompareHashAndPassword([]byte(a.cfg.Password), []byte(password)); err == nil {
			return true, "admin"
		}
		return false, ""
	}

	// Check regular users.
	for _, u := range a.cfg.Users {
		if u.Username == username && !u.Disabled {
			if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err == nil {
				return true, "user"
			}
			return false, ""
		}
	}

	return false, ""
}

// GeneratePassword creates a random 32-character password and its bcrypt hash.
func GeneratePassword() (plain, hash string, err error) {
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = hex.EncodeToString(b)
	hashed, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	return plain, string(hashed), nil
}

// HashPassword returns a bcrypt hash of the given plain-text password.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(h), err
}