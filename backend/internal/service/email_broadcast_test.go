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
