package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Bound Chromium memory use independently of generation and judge concurrency.
var pelicanCreativeRenderSlot = make(chan struct{}, 1)
var pelicanCreativeFrameTimes = []int{500, 2000, 4500}

type pelicanCreativeFrames struct {
	Images []string
	Hashes []string
	Times  []int
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
	policy := `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src data:; object-src 'none'; base-uri 'none'; form-action 'none'`
	// A data URL keeps the frame opaque and inherits this CSP. Chromium can
	// suppress implicit SVG SMIL attributes in srcdoc, despite a running timeline.
	document := `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + policy + `"><style>html,body,iframe{margin:0;border:0;width:100%;height:100%;overflow:hidden}iframe{display:block}</style></head><body><iframe sandbox="allow-scripts" src="data:text/html;charset=utf-8;base64,` + base64.StdEncoding.EncodeToString([]byte(source)) + `"></iframe></body></html>`
	file := filepath.Join(work, "drawing.html")
	if err := os.WriteFile(file, []byte(document), 0600); err != nil {
		return nil, err
	}
	profile := filepath.Join(work, "profile")
	cmd := exec.CommandContext(ctx, binary,
		"--headless", "--no-sandbox", "--no-zygote", "--renderer-process-limit=1", "--disable-gpu", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-extensions", "--disable-component-update", "--disable-sync",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding", "--disable-backgrounding-occluded-windows",
		"--no-first-run", "--no-default-browser-check", "--hide-scrollbars", "--window-size=960,720",
		"--host-resolver-rules=MAP * ~NOTFOUND", "--proxy-server=http://127.0.0.1:9", "--proxy-bypass-list=<-loopback>",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--user-data-dir="+profile, "about:blank")
	cmd.WaitDelay = 2 * time.Second
	configureCreativeCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("creative rendering failed: %w", err)
	}
	defer func() {
		_ = cmd.Cancel()
		_ = cmd.Wait()
	}()
	// Keep one document alive for the whole sequence. Reloading for each frame
	// would mistake random static initialization for animation.
	cdp, err := connectCreativeCDP(ctx, profile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cdp.conn.CloseNow() }()
	var target struct{ TargetID string }
	if err := cdp.call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		return nil, err
	}
	var session struct{ SessionID string }
	if err := cdp.call(ctx, "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &session); err != nil {
		return nil, err
	}
	cdp.session = session.SessionID
	if err := cdp.call(ctx, "Page.enable", nil, nil); err != nil {
		return nil, err
	}
	if err := cdp.call(ctx, "Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}, nil); err != nil {
		return nil, err
	}
	if err := cdp.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 960, "height": 720, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
		return nil, err
	}
	var navigation struct{ LoaderID, ErrorText string }
	if err := cdp.call(ctx, "Page.navigate", map[string]any{"url": "file://" + filepath.ToSlash(file)}, &navigation); err != nil {
		return nil, err
	}
	if navigation.ErrorText != "" || navigation.LoaderID == "" {
		return nil, fmt.Errorf("creative navigation failed: %s", navigation.ErrorText)
	}
	for cdp.loaded != navigation.LoaderID {
		if _, err := cdp.read(ctx); err != nil {
			return nil, err
		}
	}
	// Activating after navigation is required for SVG SMIL timelines in the
	// sandboxed frame; activating the blank page can leave them suspended.
	if err := cdp.call(ctx, "Page.bringToFront", nil, nil); err != nil {
		return nil, err
	}
	started := time.Now()
	frames := &pelicanCreativeFrames{}
	var previous image.Image
	for _, ms := range pelicanCreativeFrameTimes {
		timer := time.NewTimer(time.Until(started.Add(time.Duration(ms) * time.Millisecond)))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
		var shot struct{ Data string }
		sampledAt := int(time.Since(started).Milliseconds())
		if err := cdp.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false,
			"clip": map[string]any{"x": 0, "y": 0, "width": 960, "height": 720, "scale": 1}}, &shot); err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(shot.Data)
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
		frames.Times = append(frames.Times, sampledAt)
		frames.Images = append(frames.Images, "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(encoded.Bytes()))
	}
	return frames, nil
}

// Only sequential commands are needed by this private, short-lived renderer.
type creativeCDP struct {
	conn    *websocket.Conn
	seq     int
	session string
	loaded  string
}

type creativeCDPMessage struct {
	ID        int             `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Result    json.RawMessage `json:"result"`
	Params    struct {
		Name, LoaderID string
	} `json:"params"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func connectCreativeCDP(ctx context.Context, profile string) (*creativeCDP, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		contents, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		lines := strings.Fields(string(contents))
		if err == nil && len(lines) == 2 {
			port, err := strconv.Atoi(lines[0])
			if err != nil || port < 1 || port > 65535 || !strings.HasPrefix(lines[1], "/devtools/browser/") {
				return nil, fmt.Errorf("invalid creative renderer endpoint")
			}
			conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+lines[0]+lines[1], nil)
			if err != nil {
				return nil, fmt.Errorf("creative renderer connection failed: %w", err)
			}
			conn.SetReadLimit(12 << 20)
			return &creativeCDP{conn: conn}, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (cdp *creativeCDP) read(ctx context.Context) (*creativeCDPMessage, error) {
	var message creativeCDPMessage
	if err := wsjson.Read(ctx, cdp.conn, &message); err != nil {
		return nil, fmt.Errorf("creative renderer response failed: %w", err)
	}
	if message.SessionID == cdp.session && message.Method == "Page.lifecycleEvent" && message.Params.Name == "load" {
		cdp.loaded = message.Params.LoaderID
	}
	return &message, nil
}

func (cdp *creativeCDP) call(ctx context.Context, method string, params any, result any) error {
	cdp.seq++
	command := map[string]any{"id": cdp.seq, "method": method}
	if params != nil {
		command["params"] = params
	}
	if cdp.session != "" {
		command["sessionId"] = cdp.session
	}
	if err := wsjson.Write(ctx, cdp.conn, command); err != nil {
		return err
	}
	for {
		message, err := cdp.read(ctx)
		if err != nil {
			return err
		}
		if message.ID != cdp.seq {
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("creative renderer %s: %s", method, message.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(message.Result, result)
		}
		return nil
	}
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
