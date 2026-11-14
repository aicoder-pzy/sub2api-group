package httputil

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"testing/iotest"
)

func gzipBodyForTest(t *testing.T, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestGzipBodyStreamsBeforeWireEOF(t *testing.T) {
	// A flushed, unfinished member expands past the memory budget. The decoder
	// must reject it before trying to read more network bytes, not buffer it all.
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	_, _ = w.Write(bytes.Repeat([]byte{'x'}, 256<<10))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	prefix := bytes.Clone(out.Bytes())
	_ = w.Close()
	late := errors.New("decoder waited for the complete wire body")
	budget := NewBodyMemoryBudget(64 << 10)
	ctx, release := WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	req := newRequestWithBody(t, nil, "gzip").WithContext(ctx)
	req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), iotest.ErrReader(late)))
	req.ContentLength = -1
	body, err := ReadRequestBodyWithPrealloc(req)
	if body != nil || !errors.Is(err, ErrRequestBodyMemoryBudget) {
		t.Fatalf("did not decode incrementally: %v", err)
	}
	if used := requestBodyBudgetUsed(budget); used != 0 {
		t.Fatalf("failed decode retained %d bytes", used)
	}
}

func TestGzipBodyPreservesWireFailures(t *testing.T) {
	plain := bytes.Repeat([]byte("unchanged"), 1024)
	encoded := gzipBodyForTest(t, plain)
	for _, name := range []string{"deadline", "disconnect", "late_error", "wire_limit", "checksum", "trailing_garbage"} {
		t.Run(name, func(t *testing.T) {
			budget := NewBodyMemoryBudget(2 << 20)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, release := WithRequestBodyMemoryBudget(parent, budget)
			defer release()
			req := newRequestWithBody(t, encoded, "gzip").WithContext(ctx)
			kind := RequestBodyReadErrorDecodeContentEncoding
			status := http.StatusBadRequest
			switch name {
			case "deadline", "disconnect", "late_error":
				cause := io.ErrUnexpectedEOF
				kind = RequestBodyReadErrorTruncated
				if name == "deadline" {
					cause = os.ErrDeadlineExceeded
					kind = RequestBodyReadErrorTimeout
					status = http.StatusRequestTimeout
				}
				if name == "disconnect" {
					cause = context.Canceled
					kind = RequestBodyReadErrorClientDisconnect
					status = StatusClientClosedRequest
				}
				prefix := encoded[:len(encoded)/2]
				if name == "late_error" {
					prefix = encoded
				}
				req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), requestBodyBudgetReaderFunc(func([]byte) (int, error) {
					if name != "late_error" {
						cancel()
					}
					return 0, cause
				})))
			case "wire_limit":
				req.Body = http.MaxBytesReader(nil, req.Body, int64(len(encoded)-1))
				kind = RequestBodyReadErrorMaxBytes
				status = http.StatusRequestEntityTooLarge
			case "checksum":
				damaged := bytes.Clone(encoded)
				damaged[len(damaged)-8] ^= 1
				req.Body = io.NopCloser(bytes.NewReader(damaged))
			case "trailing_garbage":
				req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(encoded), bytes.NewReader([]byte("garbage"))))
			}
			body, err := ReadRequestBodyWithPrealloc(req)
			if err == nil || body != nil {
				t.Fatalf("accepted damaged/failed upload: %v", err)
			}
			failure := DescribeRequestBodyReadFailure(req, err)
			if failure.Kind != kind || failure.Status != status {
				t.Fatalf("failure=%+v, want %s/%d (%v)", failure, kind, status, err)
			}
			if used := requestBodyBudgetUsed(budget); used != 0 {
				t.Fatalf("failed request retained %d bytes", used)
			}
			if req.Header.Get("Content-Encoding") != "gzip" {
				t.Fatal("failed request lost its encoding")
			}
		})
	}
}

func TestGzipBodyPreservesMembersAndFragmentedReads(t *testing.T) {
	one, two := []byte("first member"), bytes.Repeat([]byte("second"), 1000)
	wire := append(gzipBodyForTest(t, one), gzipBodyForTest(t, two)...)
	for _, encoding := range []string{"gzip", "x-gzip", " GZIP "} {
		req := newRequestWithBody(t, wire, encoding)
		req.Body = io.NopCloser(iotest.OneByteReader(bytes.NewReader(wire)))
		req.ContentLength = -1
		body, err := ReadRequestBodyWithPrealloc(req)
		if err != nil || !bytes.Equal(body, append(bytes.Clone(one), two...)) {
			t.Fatalf("member or fragmented read changed: %v", err)
		}
		stats, ok := RequestBodyObservation(req)
		if !ok || stats.EncodedBytes != len(wire) || stats.DecodedBytes != len(body) {
			t.Fatalf("incorrect progress: %+v", stats)
		}
	}
}
