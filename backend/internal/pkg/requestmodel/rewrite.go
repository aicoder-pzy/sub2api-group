package requestmodel

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/tidwall/gjson"
)

// RewriteJSON replaces every top-level model and session.model, including
// duplicate keys and case variants. Other bytes, including tool inputs, survive.
func RewriteJSON(body []byte, mapping map[string]string) []byte {
	return rewriteModelObject(body, mapping, true)
}

func rewriteModelObject(body []byte, mapping map[string]string, sessions bool) []byte {
	if len(mapping) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	var out []byte
	cursor := 0
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		var replacement []byte
		if strings.EqualFold(key.String(), "model") && value.Type == gjson.String {
			if mapped := mapping[strings.TrimSpace(value.String())]; mapped != "" {
				replacement, _ = json.Marshal(mapped)
			}
		} else if sessions && strings.EqualFold(key.String(), "session") && value.IsObject() {
			rewritten := rewriteModelObject([]byte(value.Raw), mapping, false)
			if !bytes.Equal(rewritten, []byte(value.Raw)) {
				replacement = rewritten
			}
		}
		if replacement != nil {
			out = append(out, body[cursor:value.Index]...)
			out = append(out, replacement...)
			cursor = value.Index + len(value.Raw)
		}
		return true
	})
	if out == nil {
		return body
	}
	return append(out, body[cursor:]...)
}

// RewriteBody also supports image/audio uploads and multipart realtime sessions.
func RewriteBody(contentType string, body []byte, mapping map[string]string) ([]byte, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return RewriteJSON(body, mapping), nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(params["boundary"]); err != nil {
		return nil, err
	}
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		if part.FileName() == "" {
			switch part.FormName() {
			case "model":
				if target := mapping[strings.TrimSpace(string(data))]; target != "" {
					data = []byte(target)
				}
			case "session":
				data = RewriteJSON(data, mapping)
			}
		}
		dst, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, err
		}
		if _, err := dst.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
