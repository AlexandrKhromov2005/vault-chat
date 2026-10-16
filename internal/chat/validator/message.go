package validator

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrInvalidText rejects empty, oversized or unsafe message text.
	ErrInvalidText = errors.New("chat: invalid message text")
	// ErrInvalidCursor rejects noncanonical history positions.
	ErrInvalidCursor = errors.New("chat: invalid history cursor")
	// ErrInvalidCatchUp rejects noncanonical catch-up positions.
	ErrInvalidCatchUp = errors.New("chat: invalid catch-up position")
)

// MessageText preserves whitespace but requires visible content and at most
// 4096 Unicode code points. Only newline and tab controls are allowed.
func MessageText(text string) error {
	if len(text) > 16384 || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 4096 || strings.TrimSpace(text) == "" {
		return ErrInvalidText
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ErrInvalidText
		}
	}
	return nil
}

// HistoryCursor accepts an empty first-page cursor or a positive room sequence.
func HistoryCursor(input string) (int64, error) {
	if input == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(input, 10, 64)
	if err != nil || value <= 0 || FormatHistoryCursor(value) != input {
		return 0, ErrInvalidCursor
	}
	return value, nil
}

// CatchUpCursor accepts "0", the start of a room, or a canonical positive room
// sequence. Messages after it are read oldest first.
func CatchUpCursor(input string) (int64, error) {
	value, err := strconv.ParseInt(input, 10, 64)
	if err != nil || value < 0 || FormatHistoryCursor(value) != input {
		return 0, ErrInvalidCatchUp
	}
	return value, nil
}

// FormatHistoryCursor produces the canonical text of a room sequence position.
func FormatHistoryCursor(value int64) string { return strconv.FormatInt(value, 10) }
