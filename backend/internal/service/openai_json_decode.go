package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/tidwall/gjson"
)

// The standard validator scans ordinary strings faster on our Go toolchain.
// Keep GJSON as the fallback for its legacy UTF-8/depth acceptance behavior.
func validOpenAIRequestJSON(data []byte) bool {
	return json.Valid(data) || gjson.ValidBytes(data)
}

func decodeOpenAIJSONUseNumber(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}
