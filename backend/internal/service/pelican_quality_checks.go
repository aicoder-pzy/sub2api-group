package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type pelicanQualityChecks struct {
	nonce       string
	paths, code int
}

// Small randomized, objectively verifiable questions share the drawing request.
// They add no second generation or paid judge request.
func withPelicanQualityChecks(cfg *PelicanTestConfig) (*PelicanTestConfig, *pelicanQualityChecks, error) {
	if cfg == nil || !cfg.VerificationTasks {
		return cfg, nil, nil
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, err
	}
	n, k := int(random[0]%5)+4, int(random[1]%4)+2
	paths := 1
	for i := 1; i <= k; i++ {
		paths = paths * (n + i) / i
	}
	values := []int{int(random[2]%9) + 1, int(random[3]%9) + 1, int(random[4]%9) + 1, int(random[5]%9) + 1}
	code := 0
	for i, value := range values {
		if value%2 != 0 {
			code += (i + 1) * value
		}
	}
	check := &pelicanQualityChecks{nonce: hex.EncodeToString(random[:]), paths: paths, code: code}
	copy := *cfg
	copy.Prompt += fmt.Sprintf("\n附加两道独立能力核验，不改变作品：\n1. 从(0,0)到(%d,%d)，每步只能向右或向上移动1格，共有多少条路径？\n2. Python 3 表达式 sum((i+1)*x for i,x in enumerate(%v) if x%%2) 的值是多少？\n在 HTML 的 </body> 前加入一条注释，严格格式：<!--quality-check:%s:{\"paths\":第一题整数答案,\"code\":第二题整数答案}-->。不要将题目或答案画在作品上。", n, k, values, check.nonce)
	// Go's list syntax has spaces; Python requires comma-separated elements.
	copy.Prompt = strings.Replace(copy.Prompt, fmt.Sprint(values), fmt.Sprintf("[%d,%d,%d,%d]", values[0], values[1], values[2], values[3]), 1)
	return &copy, check, nil
}

func (check *pelicanQualityChecks) assess(output string, assessment *PelicanAssessment) *PelicanAssessment {
	if check == nil {
		return assessment
	}
	value := &PelicanAssessment{Quality: "unknown", Reason: "附加能力核验缺少有效答案", Source: "sub2api:quality-checks"}
	pattern := regexp.MustCompile(`<!--quality-check:` + regexp.QuoteMeta(check.nonce) + `:(\{[^\r\n]*?\})-->`)
	matches := pattern.FindAllStringSubmatch(output, 2)
	if len(matches) == 1 {
		var answer struct {
			Paths *int `json:"paths"`
			Code  *int `json:"code"`
		}
		if json.Unmarshal([]byte(matches[0][1]), &answer) == nil && answer.Paths != nil && answer.Code != nil {
			if *answer.Paths != check.paths || *answer.Code != check.code {
				value.Quality = "degraded"
				value.Reason = "附加路径计数或代码求值核验未通过"
			} else {
				return assessment
			}
		}
	}
	if assessment != nil && assessment.Quality == "degraded" {
		return assessment
	}
	return value
}
