package httputil

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
)

// Mixed binary-like and repetitive content approximates large image/tool
// requests without using customer payloads. Compression is outside the timer.
func BenchmarkReadCompressedRequest(b *testing.B) {
	for _, mib := range []int{1, 10, 60} {
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			body := make([]byte, mib<<20)
			var state uint32 = 1
			for i := range body {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				if i < len(body)*3/4 {
					body[i] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"[state&63]
				} else {
					body[i] = 'x'
				}
			}
			var encoded bytes.Buffer
			w, _ := gzip.NewWriterLevel(&encoded, gzip.BestSpeed)
			_, _ = w.Write(body)
			if err := w.Close(); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx, release := WithRequestBodyMemoryBudget(context.Background(), NewBodyMemoryBudget(256<<20))
				req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(encoded.Bytes())).WithContext(ctx)
				req.Header.Set("Content-Encoding", "gzip")
				decoded, err := ReadRequestBodyWithPrealloc(req)
				if err != nil || !bytes.Equal(decoded, body) {
					b.Fatalf("body changed: %v", err)
				}
				release()
			}
		})
	}
}
