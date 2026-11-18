package admin

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestSupportsUpstreamSiteImportPlatform(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGrok} {
		if !supportsUpstreamSiteImportPlatform(platform) {
			t.Errorf("platform %q should be supported", platform)
		}
	}
	for _, platform := range []string{"gemini", "unknown"} {
		if supportsUpstreamSiteImportPlatform(platform) {
			t.Errorf("platform %q should not be supported", platform)
		}
	}
}
