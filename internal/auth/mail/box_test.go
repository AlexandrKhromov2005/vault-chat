package mail

import (
	"bytes"
	"strings"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	box, err := NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestBoxEncryptsAuthenticatesAndRandomizes(t *testing.T) {
	box := testBox(t)
	message := Message{To: "synthetic@example.invalid", Subject: "Verify", Text: "synthetic-one-use-token"}
	a, err := box.Seal(message)
	if err != nil {
		t.Fatal(err)
	}
	b, err := box.Seal(message)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, []byte(message.Text)) || bytes.Contains(a, []byte(message.To)) {
		t.Fatal("outbox payload must hide message data and use a fresh nonce")
	}
	got, err := box.Open(a)
	if err != nil || got != message {
		t.Fatalf("roundtrip failed: %v", err)
	}
	for _, corrupted := range [][]byte{nil, a[:8], append(append([]byte{}, a...), 0), bytes.Repeat([]byte{0}, 65537)} {
		if _, err := box.Open(corrupted); err == nil {
			t.Fatal("accepted malformed ciphertext")
		}
	}
	a[len(a)-1] ^= 1
	if _, err := box.Open(a); err == nil {
		t.Fatal("accepted modified authenticated ciphertext")
	}
	other, _ := NewBox(bytes.Repeat([]byte{9}, 32))
	if _, err := other.Open(b); err == nil {
		t.Fatal("accepted ciphertext with the wrong key")
	}
}

func TestBoxRejectsMalformedMessagesAndOversize(t *testing.T) {
	box := testBox(t)
	for _, key := range [][]byte{nil, make([]byte, 16), make([]byte, 31), make([]byte, 33)} {
		if _, err := NewBox(key); err == nil {
			t.Fatal("accepted non-AES-256 key")
		}
	}
	for _, message := range []Message{
		{To: "one@example.invalid,two@example.invalid", Subject: "Verify", Text: "body"},
		{To: "one@example.invalid\r\nBcc: two@example.invalid", Subject: "Verify", Text: "body"},
		{To: "one@example.invalid", Subject: "Verify\nBcc: two@example.invalid", Text: "body"},
		{To: "one@example.invalid", Subject: "Verify", Text: strings.Repeat("x", 65536)},
		{To: "one@example.invalid", Subject: string([]byte{0xff}), Text: "body"},
	} {
		if _, err := box.Seal(message); err == nil {
			t.Fatal("accepted malformed or oversized message")
		}
	}
}

func FuzzBoxOpen(f *testing.F) {
	f.Add([]byte("synthetic malformed ciphertext"))
	box, _ := NewBox(bytes.Repeat([]byte{1}, 32))
	valid, _ := box.Seal(Message{To: "fixture@example.invalid", Subject: "Verify", Text: "synthetic-token"})
	f.Add(valid)
	f.Fuzz(func(t *testing.T, data []byte) {
		message, err := box.Open(data)
		if err == nil {
			if _, err := box.Seal(message); err != nil {
				t.Fatal("opened message cannot be sealed")
			}
		}
	})
}

func FuzzMessage(f *testing.F) {
	f.Add("fixture@example.invalid", "Проверка", "synthetic-token\nBody")
	f.Add("fixture@example.invalid\r\nBcc: other@example.invalid", "Verify", "Body")
	f.Add("fixture@example.invalid", "Verify\r\nInjected: yes", "Body")
	box, _ := NewBox(bytes.Repeat([]byte{2}, 32))
	f.Fuzz(func(t *testing.T, to, subject, text string) {
		message := Message{To: to, Subject: subject, Text: text}
		payload, err := box.Seal(message)
		if err != nil {
			return
		}
		if strings.ContainsAny(to+subject, "\r\n") || len(payload) > maxPayload+29 {
			t.Fatal("accepted unsafe headers or exceeded encrypted-payload budget")
		}
		opened, err := box.Open(payload)
		if err != nil || opened != message {
			t.Fatal("valid message did not roundtrip")
		}
	})
}
