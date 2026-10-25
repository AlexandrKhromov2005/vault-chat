// Package mail encrypts queued account notifications and delivers them over TLS.
package mail

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	stdmail "net/mail"
	"strings"
	"unicode/utf8"
)

const maxPayload = 64 * 1024

var (
	errMessage = errors.New("invalid mail message")
	errBox     = errors.New("mail payload unavailable")
	boxContext = []byte("vault-chat/account-mail/v1")
)

// Message contains exactly one recipient and a UTF-8 plain-text notification.
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// Box protects notification contents at rest with AES-256-GCM.
type Box struct{ aead cipher.AEAD }

// NewBox requires a dedicated 32-byte encryption key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errBox
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errBox
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errBox
	}
	return &Box{aead: aead}, nil
}

// Seal validates and encrypts at most 64 KiB of serialized message data.
func (b *Box) Seal(message Message) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errBox
	}
	data, err := encodeMessage(message)
	if err != nil {
		return nil, errBox
	}
	sealed := make([]byte, 1+b.aead.NonceSize(), 1+b.aead.NonceSize()+len(data)+b.aead.Overhead())
	sealed[0] = 1
	nonce := sealed[1:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, errBox
	}
	return b.aead.Seal(sealed, nonce, data, boxContext), nil
}

// Open authenticates the versioned ciphertext before decoding its message.
// All malformed, oversized and unauthenticated payloads produce the same error.
func (b *Box) Open(sealed []byte) (Message, error) {
	empty := Message{}
	if b == nil || b.aead == nil {
		return empty, errBox
	}
	header := 1 + b.aead.NonceSize()
	if len(sealed) < header+b.aead.Overhead() || len(sealed) > header+maxPayload+b.aead.Overhead() || sealed[0] != 1 {
		return empty, errBox
	}
	data, err := b.aead.Open(nil, sealed[1:header], sealed[header:], boxContext)
	if err != nil {
		return empty, errBox
	}
	var message Message
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return empty, errBox
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return empty, errBox
	}
	if _, err := encodeMessage(message); err != nil {
		return empty, errBox
	}
	return message, nil
}

func encodeMessage(message Message) ([]byte, error) {
	if len(message.To)+len(message.Subject)+len(message.Text) > maxPayload ||
		len(message.Subject) == 0 || len(message.Subject) > 200 ||
		!utf8.ValidString(message.Subject) || !utf8.ValidString(message.Text) ||
		hasControl(message.Subject) || strings.ContainsRune(message.Text, 0) {
		return nil, errMessage
	}
	if _, err := mailbox(message.To, false); err != nil {
		return nil, errMessage
	}
	data, err := json.Marshal(message)
	if err != nil || len(data) > maxPayload {
		return nil, errMessage
	}
	return data, nil
}

func mailbox(value string, displayName bool) (*stdmail.Address, error) {
	if len(value) == 0 || len(value) > 254 || !utf8.ValidString(value) || hasControl(value) {
		return nil, errMessage
	}
	address, err := stdmail.ParseAddress(value)
	if err != nil || !strings.Contains(address.Address, "@") {
		return nil, errMessage
	}
	if !displayName && (address.Name != "" || address.Address != value) {
		return nil, errMessage
	}
	for _, char := range address.Address {
		if char > 127 {
			return nil, errMessage
		}
	}
	return address, nil
}

func hasControl(value string) bool {
	for _, char := range value {
		if char < 32 || char == 127 {
			return true
		}
	}
	return false
}
