package service

import (
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (responseOut *http.Response, errorOut error) {
	if err := reserveFastestFailoverSubmission(request.Context(), account.ID); err != nil {
		return nil, err
	}
	if attempt := fastestFailoverAttemptFromContext(request.Context()); attempt != nil {
		request = request.WithContext(attempt.bind(request.Context()))
		defer func() {
			if responseOut != nil && responseOut.Body != nil {
				if responseOut.Request == nil {
					responseOut.Request = request
				}
				responseOut.Body = attempt.wrapBody(responseOut.Body)
			}
		}()
	}
	if response, handled, err := s.routeBPSTicketHTTP(request, account); handled {
		return response, err
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return doSchedulingProxyFallback(request, account, proxyURL, schedulingProxyRepository(s.settingService),
		func(id int64) bool {
			return account.Platform == PlatformOpenAI && s.getOpenAIProxyStreamCircuit().isBlocked(id, time.Now())
		},
		func(attempt *http.Request, url string) (*http.Response, error) {
			return s.httpUpstream.Do(attempt, url, account.ID, account.Concurrency)
		})
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.openaiGatewayService != nil {
		if response, handled, err := s.openaiGatewayService.routeBPSTicketHTTP(request, account); handled {
			return response, err
		}
	}
	if s.pluginManager != nil {
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(request.Context(), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		var profile *tlsfingerprint.Profile
		if s.tlsFPProfileService != nil {
			profile = s.tlsFPProfileService.ResolveTLSProfile(account)
		}
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			profile,
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}
