//go:build compose

package compose

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCompose_AuthorizedImagePreview(t *testing.T) {
	if os.Getenv("VAULT_CHAT_COMPOSE_PREVIEW") != "1" {
		t.Skip("preview worker overlay is not enabled")
	}
	base := gatewayURL(t)
	waitReady(t, base)
	api := base + "/api/v1"
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	_, owner := signUp(t, api, "preview_a_"+suffix)
	peerID, peer := signUp(t, api, "preview_b_"+suffix)
	_, outsider := signUp(t, api, "preview_c_"+suffix)
	code, room := post(t, api+"/rooms/channels", owner, `{"name":"previews","private":true}`)
	require.Equal(t, 201, code, room)
	roomID := room["room_id"].(string)
	code, _ = fileRequest(t, http.MethodPut, api+"/rooms/"+roomID+"/members/"+peerID, owner)
	require.Equal(t, 204, code)

	original := image.NewNRGBA(image.Rect(0, 0, 640, 320))
	for y := range 320 {
		for x := range 640 {
			original.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 127, A: 200})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, original))
	code, file := uploadFile(t, api+"/rooms/"+roomID+"/files", owner, "photo.png", encoded.Bytes())
	require.Equal(t, 201, code, file)
	id := file["file_id"].(string)
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id+"/preview", outsider)
	require.Equal(t, 404, code)

	var preview []byte
	require.Eventually(t, func() bool {
		code, preview = fileRequest(t, http.MethodGet, api+"/files/"+id+"/preview", peer)
		return code == 200
	}, 20*time.Second, 250*time.Millisecond, "asynchronous preview did not become ready")
	require.LessOrEqual(t, len(preview), 256<<10)
	img, err := jpeg.Decode(bytes.NewReader(preview))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 320, 160), img.Bounds())
	require.NotContains(t, preview, []byte("Exif"))

	req, err := http.NewRequest(http.MethodGet, api+"/files/"+id+"/preview", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+peer)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "image/jpeg", resp.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "private, no-store", resp.Header.Get("Cache-Control"))

	code, originalData := fileRequest(t, http.MethodGet, api+"/files/"+id+"/content", owner)
	require.Equal(t, 200, code)
	require.Equal(t, encoded.Bytes(), originalData)
	code, _ = fileRequest(t, http.MethodDelete, api+"/rooms/"+roomID+"/members/"+peerID, owner)
	require.Equal(t, 204, code)
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id+"/preview", peer)
	require.Equal(t, 404, code)
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id+"/preview", owner)
	require.Equal(t, 200, code)
}
