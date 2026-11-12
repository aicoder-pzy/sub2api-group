package repository

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Large requests retain only the gzip window and a pipe, rather than another
// original-sized replay buffer and a complete compressed copy. Keep at most two
// encoders active; ordinary requests fall back to identity when both are busy.
func gzipStreamingUpstreamRequest(req *http.Request) *http.Request {
	open := func(wait bool) (io.ReadCloser, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		if wait {
			select {
			case upstreamGzipSlots <- struct{}{}:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		} else {
			select {
			case upstreamGzipSlots <- struct{}{}:
			default:
				return nil, errors.New("upstream gzip encoders busy")
			}
		}
		source, err := req.GetBody()
		if err != nil || source == nil {
			if source != nil {
				_ = source.Close()
			}
			<-upstreamGzipSlots
			if err == nil {
				err = errors.New("upstream gzip replay body unavailable")
			}
			return nil, err
		}
		reader, writer := io.Pipe()
		return &upstreamGzipStream{ctx: req.Context(), source: source, reader: reader, writer: writer, size: req.ContentLength}, nil
	}
	body, err := open(false)
	if err != nil {
		return req
	}
	clone := req.Clone(req.Context())
	clone.Body = body
	// HTTP transport replays keep the same gzip representation and honor request
	// cancellation while waiting for an encoder freed by the previous attempt.
	clone.GetBody = func() (io.ReadCloser, error) { return open(true) }
	clone.ContentLength = -1
	clone.TransferEncoding = nil
	for key := range clone.Header {
		if strings.EqualFold(key, "Content-Length") {
			delete(clone.Header, key)
		}
	}
	clone.Header.Set("Content-Encoding", "gzip")
	return clone
}

type upstreamGzipStream struct {
	ctx          context.Context
	source       io.ReadCloser
	reader       *io.PipeReader
	writer       *io.PipeWriter
	size         int64
	start        sync.Once
	closeSource  sync.Once
	closeErr     error
	encodedBytes atomic.Int64
}

func (b *upstreamGzipStream) closeInput() error {
	b.closeSource.Do(func() { b.closeErr = b.source.Close() })
	return b.closeErr
}

func (b *upstreamGzipStream) Read(p []byte) (int, error) {
	b.start.Do(func() { go b.encode() })
	n, err := b.reader.Read(p)
	b.encodedBytes.Add(int64(n))
	return n, err
}

func (b *upstreamGzipStream) Close() error {
	// A body closed before its first Read never starts an encoder goroutine.
	b.start.Do(func() { <-upstreamGzipSlots })
	_ = b.reader.Close()
	return b.closeInput()
}

func (b *upstreamGzipStream) encode() {
	defer func() { <-upstreamGzipSlots }()
	defer func() { _ = b.closeInput() }()
	stop := context.AfterFunc(b.ctx, func() {
		_ = b.reader.CloseWithError(b.ctx.Err())
		_ = b.closeInput()
	})
	defer stop()
	writer, _ := upstreamGzipWriters.Get().(*gzip.Writer)
	writer.Reset(b.writer)
	written, err := io.Copy(writer, io.LimitReader(b.source, b.size+1))
	if err == nil && written != b.size {
		err = io.ErrUnexpectedEOF
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	writer.Reset(io.Discard)
	upstreamGzipWriters.Put(writer)
	_ = b.writer.CloseWithError(err)
}
