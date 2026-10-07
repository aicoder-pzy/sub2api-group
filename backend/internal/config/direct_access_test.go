package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectAccessHostFromEnvironment(test *testing.T) {
	resetViperWithJWTSecret(test)
	test.Setenv("SERVER_DIRECT_ACCESS_HOST", "direct.easygpt.top")
	cfg, err := Load()
	require.NoError(test, err)
	require.Equal(test, "direct.easygpt.top", cfg.Server.DirectAccessHost)
}
