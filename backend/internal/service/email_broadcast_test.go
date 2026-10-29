package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailBroadcastRecipientConversion(t *testing.T) {
	p, err := ParseEmailBroadcastRecipients("\ufeff123456789,123456789@qq.com； USER@EXAMPLE.COM\n987654321\tuser@example.com;备注123456789;012345;1234;1234567890123;Name<user@example.com>")
	require.NoError(t, err)
	require.Equal(t, []string{"123456789@qq.com", "user@example.com", "987654321@qq.com"}, p.Recipients)
	require.Equal(t, 2, p.Duplicates)
	require.Equal(t, []string{"备注123456789", "012345", "1234", "1234567890123", "Name<user@example.com>"}, p.Invalid)
	_, err = ParseEmailBroadcastRecipients(strings.Repeat("1", emailBroadcastMaxInput+1))
	require.Error(t, err)
	_, err = ParseEmailBroadcastRecipients(strings.Repeat("12345 ", EmailBroadcastMaxRecipients*2+1))
	require.Error(t, err)
	p, err = ParseEmailBroadcastRecipients("x@example.com\r\nBcc:attacker@example.com")
	require.NoError(t, err)
	require.Len(t, p.Invalid, 1)
	require.Equal(t, []string{"x@example.com"}, p.Recipients)
	require.Error(t, validateEmailBroadcastMessage("hello\r\nBcc:bad", "body"))
	require.Error(t, validateEmailBroadcastMessage("subject", strings.Repeat("中", 33334)))
	require.Contains(t, emailBroadcastHTML("<script>alert(1)</script>\nnext"), "&lt;script&gt;")
	require.NotContains(t, emailBroadcastHTML("<script>alert(1)</script>"), "<script>")
}

func TestEmailBroadcastContentFormats(t *testing.T) {
	plain, err := emailBroadcastBody("<b>plain</b>", "text")
	require.NoError(t, err)
	require.Contains(t, plain, "&lt;b&gt;plain&lt;/b&gt;")

	html, err := emailBroadcastBody("<b>rich</b>", "html")
	require.NoError(t, err)
	require.Equal(t, "<b>rich</b>", html)

	_, err = emailBroadcastBody("<a href=\"{{unsubscribe_url}}\">unsubscribe</a>", "html")
	require.Error(t, err)
	_, err = emailBroadcastBody("body", "markdown")
	require.Error(t, err)

	resolved, err := resolveEmailBroadcastUnsubscribeURL(`<a href="{{unsubscribe_url}}">unsubscribe</a>`, "https://example.com/unsubscribe?a=1&b=2")
	require.NoError(t, err)
	require.Equal(t, `<a href="https://example.com/unsubscribe?a=1&amp;b=2">unsubscribe</a>`, resolved)
	for _, invalid := range []string{"http://example.com/unsubscribe", "javascript:alert(1)", "https://user:pass@example.com/unsubscribe"} {
		_, err = resolveEmailBroadcastUnsubscribeURL(`<a href="{{unsubscribe_url}}">unsubscribe</a>`, invalid)
		require.Error(t, err)
	}
}
