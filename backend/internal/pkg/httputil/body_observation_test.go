package httputil

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBodyObservationPreservesWireEncodingThroughPreread(t *testing.T) {
	original := bytes.Repeat([]byte(`{"input":"unchanged"}`), 1000)
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	_, err := writer.Write(original)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := newRequestWithBody(t, encoded.Bytes(), "gzip")
	decoded, err := ReadRequestBodyWithPrealloc(req)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
	stats, ok := RequestBodyObservation(req)
	require.True(t, ok)
	require.Equal(t, "gzip", stats.Encoding)
	require.Equal(t, encoded.Len(), stats.EncodedBytes)
	require.Equal(t, len(original), stats.DecodedBytes)
	require.Empty(t, req.Header.Get("Content-Encoding"))
	req.Body = NewPrereadBody(decoded)
	_, err = ReadRequestBodyWithPrealloc(req)
	require.NoError(t, err)
	after, ok := RequestBodyObservation(req)
	require.True(t, ok)
	require.Equal(t, stats, after)
}

func TestBodyObservationDoesNotInventDecodedBytesOnFailure(t *testing.T) {
	req := newRequestWithBody(t, []byte("not gzip"), "gzip")
	_, err := ReadRequestBodyWithPrealloc(req)
	require.Error(t, err)
	stats, ok := RequestBodyObservation(req)
	require.True(t, ok)
	require.Equal(t, len("not gzip"), stats.EncodedBytes)
	require.Equal(t, -1, stats.DecodedBytes)
}
