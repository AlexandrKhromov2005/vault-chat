package validator

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"path"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

func TestFilename(t *testing.T) {
	for _, name := range []string{"photo.png", "отчёт.pdf", "notes.txt"} {
		require.NoError(t, Filename(name))
	}
	for _, name := range []string{"", ".", "../a.txt", "a/b.txt", `a\b.txt`, "x\r\n.txt", " x.txt", "x.txt ", "a\x00.txt", "report\u202efdp.exe", "a\u2066.txt", "a\u200b.txt", "file.txt:payload.js", "file.txt."} {
		require.Error(t, Filename(name), name)
	}
}

func TestDownloadFilename(t *testing.T) {
	for _, tt := range []struct{ name, typ, want string }{
		{"notes.txt", "text/plain; charset=utf-8", "notes.txt"},
		{"image.JPEG", "image/jpeg", "image.JPEG"},
		{"Инструкция.js", "text/plain; charset=utf-8", "Инструкция.js.txt"},
		{"payload.hta", "text/plain; charset=utf-8", "payload.hta.txt"},
		{"payload.bat", "text/plain; charset=utf-8", "payload.bat.txt"},
		{"payload.ps1", "text/plain; charset=utf-8", "payload.ps1.txt"},
		{"report.pdf", "text/plain; charset=utf-8", "report.pdf.txt"},
		{"report\u202efdp.exe", "application/pdf", "attachment.pdf"},
		{"file.txt:payload.js", "text/plain; charset=utf-8", "attachment.txt"},
		{strings.Repeat("я", 125) + ".exe", "application/pdf", "attachment.pdf"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, DownloadFilename(tt.name, tt.typ))
		})
	}
}

func FuzzDownloadFilename(f *testing.F) {
	f.Add("report.js", "text/plain; charset=utf-8")
	f.Add("report\u202efdp.exe", "application/pdf")
	f.Fuzz(func(t *testing.T, name, typ string) {
		got := DownloadFilename(name, typ)
		require.NoError(t, Filename(got))
		require.Contains(t, []string{".txt", ".pdf", ".png", ".jpg", ".jpeg"}, strings.ToLower(path.Ext(got)))
		for _, r := range got {
			require.False(t, unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
		}
	})
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

func TestImageContent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, format := range []string{"png", "jpeg"} {
		var b bytes.Buffer
		var err error
		if format == "png" {
			err = png.Encode(&b, img)
		} else {
			err = jpeg.Encode(&b, img, nil)
		}
		require.NoError(t, err)
		typ, err := Content(b.Bytes())
		require.NoError(t, err)
		require.Equal(t, "image/"+format, typ)
	}
	_, err := Content([]byte("\x89PNG\r\n\x1a\ncorrupt"))
	require.ErrorIs(t, err, ErrInvalidFile)
	_, err = Content([]byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"))
	require.Error(t, err)
}
