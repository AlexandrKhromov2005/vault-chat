//go:build compose

package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func uploadFile(t *testing.T, url, token, name string, data []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req, err := http.NewRequest(http.MethodPost, url, &body)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return resp.StatusCode, result
}
func fileRequest(t *testing.T, method, url, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, data
}

func TestCompose_PrivateAttachments(t *testing.T) {
	base := gatewayURL(t)
	waitReady(t, base)
	api := base + "/api/v1"
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	_, owner := signUp(t, api, "media_a_"+suffix)
	peerID, peer := signUp(t, api, "media_b_"+suffix)
	_, outsider := signUp(t, api, "media_c_"+suffix)
	code, room := post(t, api+"/rooms/channels", owner, `{"name":"attachments","private":true}`)
	require.Equal(t, 201, code, room)
	roomID := room["room_id"].(string)
	code, _ = fileRequest(t, http.MethodPut, api+"/rooms/"+roomID+"/members/"+peerID, owner)
	require.Equal(t, 204, code)
	conn := connect(t, base, peer)
	code, file := uploadFile(t, api+"/rooms/"+roomID+"/files", owner, "hello.txt", []byte("hello attachment"))
	require.Equal(t, 201, code, file)
	id := file["file_id"].(string)
	key := uuid.NewString()
	body := `{"client_message_id":"` + key + `","text":"","attachment_ids":["` + id + `"]}`
	code, message := post(t, api+"/rooms/"+roomID+"/messages", owner, body)
	require.Equal(t, 200, code, message)
	require.Equal(t, []any{id}, message["attachment_ids"])
	code, retry := post(t, api+"/rooms/"+roomID+"/messages", owner, body)
	require.Equal(t, 200, code, retry)
	require.Equal(t, message, retry)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err)
		var frame map[string]any
		require.NoError(t, json.Unmarshal(data, &frame))
		if frame["type"] != "message.created" {
			continue
		}
		require.Equal(t, []any{id}, frame["message"].(map[string]any)["attachment_ids"])
		break
	}
	code, data := fileRequest(t, http.MethodGet, api+"/rooms/"+roomID+"/messages", peer)
	require.Equal(t, 200, code)
	require.Contains(t, string(data), id)
	code, data = fileRequest(t, http.MethodGet, api+"/files/"+id+"/content", peer)
	require.Equal(t, 200, code)
	require.Equal(t, "hello attachment", string(data))
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id, outsider)
	require.Equal(t, 404, code)
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id+"/content", outsider)
	require.Equal(t, 404, code)
	code, _ = uploadFile(t, api+"/rooms/"+roomID+"/files", outsider, "no.txt", []byte("no"))
	require.Equal(t, 404, code)
	code, _ = post(t, api+"/rooms/"+roomID+"/messages", peer, `{"client_message_id":"`+uuid.NewString()+`","attachment_ids":["`+id+`"]}`)
	require.Equal(t, 400, code)
	code, other := post(t, api+"/rooms/channels", owner, `{"name":"other","private":true}`)
	require.Equal(t, 201, code, other)
	code, _ = post(t, api+"/rooms/"+other["room_id"].(string)+"/messages", owner, `{"client_message_id":"`+uuid.NewString()+`","attachment_ids":["`+id+`"]}`)
	require.Equal(t, 400, code)
	code, _ = uploadFile(t, api+"/rooms/"+roomID+"/files", owner, "active.html", []byte("<!DOCTYPE html><html><script>alert(1)</script></html>"))
	require.Equal(t, 400, code)
	code, _ = uploadFile(t, api+"/rooms/"+roomID+"/files", owner, "big.txt", bytes.Repeat([]byte("x"), (10<<20)+1))
	require.Equal(t, 413, code)
	code, _ = fileRequest(t, http.MethodDelete, api+"/rooms/"+roomID+"/members/"+peerID, owner)
	require.Equal(t, 204, code)
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id+"/content", peer)
	require.Equal(t, 404, code)
	code, _ = fileRequest(t, http.MethodGet, api+"/files/"+id+"/content", owner)
	require.Equal(t, 200, code)
}
