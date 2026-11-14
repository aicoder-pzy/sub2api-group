package httputil

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Track the wire reader separately from the decoder so a client timeout is
// still a transport failure, not a malformed gzip response. Read time excludes
// decoder work; the two measurements remain additive even while streaming.
type gzipRequestBodyReader struct {
	ctx    context.Context
	reader io.Reader
	bytes  int64
	read   time.Duration
	err    error
}

func (r *gzipRequestBodyReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return 0, err
	}
	start := time.Now()
	n, err := r.reader.Read(p)
	r.read += time.Since(start)
	r.bytes += int64(n)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

func readGzipRequestBodyWithBudget(req *http.Request) (result requestBodyBuffer, err error) {
	start := time.Now()
	wire := &gzipRequestBodyReader{ctx: req.Context(), reader: req.Body}
	observation := BodyReadObservation{Encoding: "gzip", DecodedBytes: -1}
	defer func() {
		elapsed := time.Since(start)
		observation.EncodedBytes = int(wire.bytes)
		observation.ReadMillis = wire.read.Milliseconds()
		observation.DecodeMicros = (elapsed - wire.read).Microseconds()
		setBodyReadObservation(req, observation)
		if err != nil {
			result.release(req.Context())
			// Preserve a read deadline even when net/http canceled the context.
			if wire.err != nil {
				err = &RequestBodyReadError{Err: wire.err, BytesRead: wire.bytes, Elapsed: elapsed}
			} else if ctxErr := req.Context().Err(); ctxErr != nil {
				err = &RequestBodyReadError{Err: ctxErr, BytesRead: wire.bytes, Elapsed: elapsed}
			} else {
				err = fmt.Errorf("decode Content-Encoding %q: %w", req.Header.Get("Content-Encoding"), err)
			}
		}
	}()
	decoder, err := gzip.NewReader(wire)
	if err != nil {
		return requestBodyBuffer{}, err
	}
	defer func() { _ = decoder.Close() }()
	// The decoded allocation shares the ingress budget. The compressed body is
	// never materialized; gzip's own input and history buffers are bounded.
	result, err = readRequestBodyChunksWithBudget(req.Context(), io.LimitReader(decoder, maxDecompressedBodySize+1), requestBodyReadInitCap, -1)
	if err != nil {
		return result, err
	}
	if len(result.data) > maxDecompressedBodySize {
		return result, &http.MaxBytesError{Limit: maxDecompressedBodySize}
	}
	// Preserve the outer MaxBytesReader and late transport errors. Do not close
	// or replace req.Body here; its owner still controls the HTTP lifecycle.
	if _, err = io.Copy(io.Discard, wire); err != nil {
		return result, err
	}
	if wire.err != nil {
		return result, wire.err
	}
	if err = req.Context().Err(); err != nil {
		return result, err
	}
	observation.DecodedBytes = len(result.data)
	req.Header.Del("Content-Encoding")
	req.Header.Del("Content-Length")
	req.ContentLength = int64(len(result.data))
	return result, nil
}
