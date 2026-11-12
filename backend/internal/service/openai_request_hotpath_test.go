package service

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func FuzzValidOpenAIRequestJSONMatchesGJSON(f *testing.F) {
	for _, body := range [][]byte{
		[]byte(`{"input":[],"reasoning":{"effort":"xhigh"}}`),
		[]byte(`{"model":"first","model":"second","text":"\ud800"}`),
		[]byte(`{"input":[]} trailing`),
		[]byte(`{"input":[}`),
		[]byte(`{"input":"\u0070arameters and \u4e2d"}`),
		[]byte(strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)),
		{'{', '"', 'i', '"', ':', '"', 0xff, '"', '}'},
	} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 1<<20 {
			t.Skip()
		}
		require.Equal(t, gjson.ValidBytes(body), validOpenAIRequestJSON(body))
	})
}

func TestToolSchemaPrecheckPreservesEscapedSchemaKeys(t *testing.T) {
	for _, key := range []string{`parameters`, `para\u006deters`, `\u0070\u0061\u0072\u0061\u006d\u0065\u0074\u0065\u0072\u0073`, `input_schema`, `input\u005fschema`} {
		t.Run(key, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"function","name":"f","` + key + `":{"type":null,"required":null}}],"input":"keep \\u4e2d"}`)
			out, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
			require.NoError(t, err)
			require.True(t, changed)
			tools := gjson.GetBytes(out, "tools").Array()
			field := "parameters"
			if key == `input_schema` || key == `input\u005fschema` {
				field = "input_schema"
			}
			require.Equal(t, "object", tools[0].Get(field+".type").String())
			require.False(t, tools[0].Get(field+".required").Exists())
			require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(out, "input").Raw)
		})
	}
}

func TestToolSchemaPrecheckKeepsValidationAndOrdinaryPayload(t *testing.T) {
	body := append(largeNativeResponsesBody(1<<20), ' ')
	out, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
	require.NoError(t, err)
	require.False(t, changed)
	require.True(t, bytes.Equal(body, out))
	for _, invalid := range [][]byte{
		[]byte(`{"input":[]} trailing`),
		[]byte(`{"input":[}`),
		[]byte("{\"input\":\"bad\x00\"}"),
		{'{', '"', 'i', '"', ':', '"', 0xff, '"', '}'},
	} {
		_, _, err := sanitizeOpenAIResponsesToolSchemasForPlatform(invalid, PlatformOpenAI)
		require.Error(t, err)
	}
}
