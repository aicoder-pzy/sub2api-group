package httputil

import (
	"context"
	"net/http"
)

// BodyReadObservation describes the body actually read at the shared decoder.
// DecodedBytes is -1 when decoding failed. Headers can already have been cleared.
type BodyReadObservation struct {
	Encoding     string
	EncodedBytes int
	DecodedBytes int
	ReadMillis   int64
	DecodeMicros int64
}

type bodyReadObservationKey struct{}

// RequestBodyObservation preserves the original encoding across preread/refill.
func RequestBodyObservation(req *http.Request) (BodyReadObservation, bool) {
	if req == nil {
		return BodyReadObservation{}, false
	}
	observation, ok := req.Context().Value(bodyReadObservationKey{}).(BodyReadObservation)
	return observation, ok
}

func setBodyReadObservation(req *http.Request, observation BodyReadObservation) {
	*req = *req.WithContext(context.WithValue(req.Context(), bodyReadObservationKey{}, observation))
}

func bodyEncodingCategory(encoding string) string {
	switch encoding {
	case "", "identity":
		return "identity"
	case "gzip", "x-gzip":
		return "gzip"
	case "deflate", "zstd":
		return encoding
	default:
		return "other"
	}
}
