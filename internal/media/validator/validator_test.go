package validator

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFilename(t *testing.T) {
	for _, name := range []string{"photo.png", "отчёт.pdf", "notes.txt"} {
		require.NoError(t, Filename(name))
	}
	for _, name := range []string{"", ".", "../a.txt", "a/b.txt", `a\b.txt`, "x\r\n.txt", " x.txt", "x.txt ", "a\x00.txt"} {
		require.Error(t, Filename(name), name)
	}
}
func TestContent(t *testing.T) {
	for _, tt := range []struct {
		data []byte
		want string
	}{{[]byte("hello мир"), "text/plain; charset=utf-8"}, {[]byte("%PDF-1.7\nbody"), "application/pdf"}} {
		got, err := Content(tt.data)
		require.NoError(t, err)
		require.Equal(t, tt.want, got)
	}
	for _, data := range [][]byte{nil, []byte("MZ\x00\x00payload"), {0xff, 0xfe, 0x00}, make([]byte, MaxBytes+1)} {
		_, err := Content(data)
		require.Error(t, err)
	}
}
func FuzzFilename(f *testing.F) {
	for _, s := range []string{"file.txt", "../bad", "", "отчёт.pdf"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if Filename(s) == nil {
			require.NotEmpty(t, s)
			require.LessOrEqual(t, len(s), 255)
		}
	})
}
func FuzzContent(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte("%PDF-1.7\n"))
	f.Add([]byte{0, 255})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxBytes+1 {
			t.Skip()
		}
		typ, err := Content(b)
		if err == nil {
			require.NotEmpty(t, typ)
			require.NotEmpty(t, b)
			require.LessOrEqual(t, len(b), MaxBytes)
		}
	})
}
