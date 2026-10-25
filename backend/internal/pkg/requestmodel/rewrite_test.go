package requestmodel

import (
	"bytes"
	"io"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewriteKeyModelJSON(t *testing.T) {
	mapping := map[string]string{"alias": "target", "target": "other"}
	body := []byte(`{"model":"alias","Model":"alias","session":{"model":"alias"},"input":{"model":"alias"},"n":9007199254740993}`)
	require.JSONEq(t, `{"model":"target","Model":"target","session":{"model":"target"},"input":{"model":"alias"},"n":9007199254740993}`, string(RewriteJSON(body, mapping)))
	duplicate := []byte(`{"model":"alias","model":"target","MODEL":"alias"}`)
	require.Equal(t, `{"model":"target","model":"other","MODEL":"target"}`, string(RewriteJSON(duplicate, mapping)))
	require.Equal(t, `{"model":"unknown"}`, string(RewriteJSON([]byte(`{"model":"unknown"}`), mapping)))
	require.Equal(t, `{"model":`, string(RewriteJSON([]byte(`{"model":`), mapping)))
}

func TestRewriteKeyModelMultipartPreservesUpload(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "alias"))
	require.NoError(t, writer.WriteField("model", "alias"))
	require.NoError(t, writer.WriteField("session", `{"model":"alias"}`))
	upload, err := writer.CreateFormFile("image", "image.png")
	require.NoError(t, err)
	original := []byte{0, 255, 13, 10, 42}
	_, err = upload.Write(original)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	out, err := RewriteBody(writer.FormDataContentType(), body.Bytes(), map[string]string{"alias": "target"})
	require.NoError(t, err)
	form, err := multipart.NewReader(bytes.NewReader(out), writer.Boundary()).ReadForm(1024)
	require.NoError(t, err)
	defer func() { _ = form.RemoveAll() }()
	require.Equal(t, []string{"target", "target"}, form.Value["model"])
	require.Equal(t, []string{`{"model":"target"}`}, form.Value["session"])
	file, err := form.File["image"][0].Open()
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	got, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, original, got)
}
