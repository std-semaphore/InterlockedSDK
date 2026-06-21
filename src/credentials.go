package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Credentials struct {
	AuthorID   string `toml:"author_id"`
	PrivateKey string `toml:"private_key"`
}

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".intsdk", "credentials.toml"), nil
}

func loadCredentials() (*Credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	var creds Credentials
	if _, err := toml.DecodeFile(path, &creds); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not registered — run: intsdk register <id>")
		}
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	if creds.AuthorID == "" || creds.PrivateKey == "" {
		return nil, fmt.Errorf("credentials incomplete — run: intsdk register <id>")
	}
	return &creds, nil
}

func saveCredentials(creds *Credentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(creds)
}

func generateKeypair() (privB64, pubB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv),
		base64.StdEncoding.EncodeToString(pub),
		nil
}

func privateKeyFromCreds(creds *Credentials) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(creds.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("invalid private key length %d", len(raw))
	}
}
