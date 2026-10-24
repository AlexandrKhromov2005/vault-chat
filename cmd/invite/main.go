// Command invite creates a single-use email-bound registration invitation.
// Run locally with AUTH_DATABASE_URL or through an operator-only container exec.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/validator"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: invite EMAIL (AUTH_DATABASE_URL required)")
	}
	email := os.Args[1]
	if err := validator.ValidateEmail(email); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("AUTH_DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("connect to Auth database: %w", err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE email=$1)", email).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("account already exists; no invitation created")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	token := hex.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().UTC().Add(24 * time.Hour)
	_, err = conn.Exec(ctx, "INSERT INTO registration_invitations (token_hash,email,expires_at) VALUES ($1,$2,$3)", hash[:], email, expires)
	if err != nil {
		return fmt.Errorf("store invitation: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Email           string    `json:"email"`
		InvitationToken string    `json:"invitation_token"`
		ExpiresAt       time.Time `json:"expires_at"`
	}{email, token, expires})
}
