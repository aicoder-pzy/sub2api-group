package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Bound Chromium memory use independently of generation and judge concurrency.
var pelicanCreativeRenderSlot = make(chan struct{}, 1)
var pelicanCreativeFrameTimes = []int{500, 2000, 4500}

type pelicanCreativeFrames struct {
	Images []string
	Hashes []string
	Motion bool
}

func creativeChromium() (string, error) {
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("creative renderer is not installed")
}

func renderPelicanCreative(ctx context.Context, output string) (*pelicanCreativeFrames, error) {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	select {
	case pelicanCreativeRenderSlot <- struct{}{}:
		defer func() { <-pelicanCreativeRenderSlot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	binary, err := creativeChromium()
	if err != nil {
		return nil, err
	}
	source := extractPelicanSource(output)
	if source == "" || len(source) > PelicanAssessmentMaxHTMLBytes {
		return nil, fmt.Errorf("invalid creative document")
	}
	work, err := os.MkdirTemp("", "pelican-creative-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	// An opaque sandbox and inherited CSP deny network, local files, popups,
	// navigation and parent access. Only the generated drawing runs in the frame.
	policy := `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src about:; object-src 'none'; base-uri 'none'; form-action 'none'`
	document := `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + policy + `"><style>html,body,iframe{margin:0;border:0;width:100%;height:100%;overflow:hidden}iframe{display:block}</style></head><body><iframe sandbox="allow-scripts" srcdoc="` + html.EscapeString(source) + `"></iframe></body></html>`
	file := filepath.Join(work, "drawing.html")
	if err := os.WriteFile(file, []byte(document), 0600); err != nil {
		return nil, err
	}
	frames := &pelicanCreativeFrames{}
	var previous image.Image
	for i, ms := range pelicanCreativeFrameTimes {
		shot := filepath.Join(work, fmt.Sprintf("frame-%d.png", i))
		cmd := exec.CommandContext(ctx, binary,
			"--headless", "--no-sandbox", "--no-zygote", "--renderer-process-limit=1", "--disable-gpu", "--disable-dev-shm-usage",
			"--disable-background-networking", "--disable-extensions", "--disable-component-update", "--disable-sync",
			"--no-first-run", "--no-default-browser-check", "--hide-scrollbars", "--window-size=960,720",
			"--host-resolver-rules=MAP * ~NOTFOUND", "--proxy-server=http://127.0.0.1:9", "--proxy-bypass-list=<-loopback>",
			"--run-all-compositor-stages-before-draw", fmt.Sprintf("--virtual-time-budget=%d", ms),
			"--user-data-dir="+filepath.Join(work, fmt.Sprintf("profile-%d", i)), "--screenshot="+shot, "file://"+filepath.ToSlash(file))
		cmd.WaitDelay = 2 * time.Second
		configureCreativeCommand(cmd)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("creative rendering failed: %w", err)
		}
		raw, err := os.ReadFile(shot)
		if err != nil || len(raw) > 8<<20 {
			return nil, fmt.Errorf("creative frame is unavailable or too large")
		}
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil || img.Bounds().Dx() != 960 || img.Bounds().Dy() != 720 {
			return nil, fmt.Errorf("invalid creative frame")
		}
		if previous != nil && creativeFramesDiffer(previous, img) {
			frames.Motion = true
		}
		previous = img
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(encoded.Bytes())
		frames.Hashes = append(frames.Hashes, hex.EncodeToString(sum[:]))
		frames.Images = append(frames.Images, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(encoded.Bytes()))
	}
	return frames, nil
}

func creativeFramesDiffer(first, next image.Image) bool {
	changed := 0
	for y := 0; y < next.Bounds().Dy(); y += 2 {
		for x := 0; x < next.Bounds().Dx(); x += 2 {
			r1, g1, b1, _ := first.At(x, y).RGBA()
			r2, g2, b2, _ := next.At(x, y).RGBA()
			if absCreativeColor(int(r1)-int(r2))+absCreativeColor(int(g1)-int(g2))+absCreativeColor(int(b1)-int(b2)) > 32*257 {
				changed++
				if changed >= 25 {
					return true
				}
			}
		}
	}
	return false
}

func absCreativeColor(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
