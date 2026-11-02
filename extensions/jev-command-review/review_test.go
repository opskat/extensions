package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/extensions/jev-command-review/jevreview"
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// 测试经 SDK 的 TestHost 调宿主专用的审核工具：设置按宿主给无资产调用的读视图预置
// （非密钥值 + 密钥只有"是否已设置"），HTTP 走它的 mock。宿主在真实运行时才往发往服务地址的
// 请求里注入 Authorization；TestHost 不注入，所以 mock 收到的请求里不应有扩展自己写的密钥头。

// configured 是用户在设置页保存过后 ctx.Settings() 读到的样子；extra 覆盖非密钥值，
// "api_key" 的布尔值覆盖密钥是否已设置。
func configured(extra map[string]any) opskat.TestOption {
	values := map[string]any{"base_url": "https://api.typesafe.ai/", "model": "jev-latest", "threshold": "0.5"}
	secrets := map[string]bool{"api_key": true}
	for k, v := range extra {
		if k == "api_key" {
			secrets[k] = v.(bool)
			continue
		}
		values[k] = v
	}
	return opskat.WithSettings(values, secrets)
}

// reviewCommand 调审核工具审一条命令，回结果。
func reviewCommand(t *testing.T, host *opskat.TestHost, item opskat.ReviewItem) opskat.ReviewResult {
	t.Helper()
	got, err := host.CallTool(opskat.Asset{}, opskat.ReviewToolName,
		map[string]string{"assetType": item.AssetType, "command": item.Command, "syntax": item.Syntax})
	require.NoError(t, err)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var out opskat.ReviewResult
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// jevAnswer 回一组是/否题的概率。
func jevAnswer(t *testing.T, w http.ResponseWriter, probs map[string]float64) {
	t.Helper()
	answers := map[string]any{}
	for id, p := range probs {
		answers[id] = map[string]any{"type": "noul", "noul": p}
	}
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"model": "jev-latest", "answers": answers}))
}

func allQuestions(p float64) map[string]float64 {
	return map[string]float64{jevreview.QuestionDestructive: p, jevreview.QuestionDisruptive: p, jevreview.QuestionRemoteCode: p}
}

var item = opskat.ReviewItem{AssetType: "ssh", Command: "rm -rf /data", Syntax: "shell"}

func TestReviewRejectsAboveThresholdAndSendsTheCommand(t *testing.T) {
	var got struct {
		auth, path string
		body       map[string]any
	}
	host := opskat.NewTestHost(configured(nil), opskat.WithMockHTTP(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.path = r.Header.Get("Authorization"), r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got.body))
		probs := allQuestions(0.01)
		probs[jevreview.QuestionDestructive] = 0.93
		jevAnswer(t, w, probs)
	}))
	defer host.Close()

	out := reviewCommand(t, host, item)
	assert.Equal(t, opskat.ReviewReject, out.Outcome)
	assert.Equal(t, []string{jevreview.QuestionDestructive}, out.Failed)
	assert.Equal(t, map[string]float64{jevreview.QuestionDestructive: 0.93}, out.Scores)

	assert.Empty(t, got.auth, "密钥头由宿主按设置的 auth 注入，扩展自己不写")
	assert.Equal(t, "/v1/systemone", got.path)
	assert.Equal(t, map[string]any{"asset_type": "ssh", "command": "rm -rf /data"}, got.body["state"])
}

func TestReviewPassesBelowThreshold(t *testing.T) {
	host := opskat.NewTestHost(configured(map[string]any{"threshold": "0.3"}),
		opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) { jevAnswer(t, w, allQuestions(0.2)) }))
	defer host.Close()

	assert.Equal(t, opskat.ReviewPass, reviewCommand(t, host, item).Outcome)
}

// 设置不完整时不发请求，按"未配置"失败，宿主据此提醒用户去填设置。
func TestReviewWithoutUsableSettingsIsNotConfigured(t *testing.T) {
	cases := map[string]opskat.TestOption{
		"从没保存过设置":    opskat.WithSettings(nil, nil),
		"没填 API key": configured(map[string]any{"api_key": false}),
		"没填服务地址":     configured(map[string]any{"base_url": ""}),
		"阈值不是 0~1":   configured(map[string]any{"threshold": "1.5"}),
		"阈值不是数":      configured(map[string]any{"threshold": "high"}),
	}
	// NaN、无穷和 0、1 及越界值都是设置错误：NaN 与任何分数比较都不成立，放行会让所有命令通过。
	for _, v := range []string{"NaN", "nan", "Inf", "-inf", "+Inf", "0", "1", "-0.1", "1.5"} {
		cases["阈值 "+v] = configured(map[string]any{"threshold": v})
	}
	for name, settings := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			host := opskat.NewTestHost(settings, opskat.WithMockHTTP(func(http.ResponseWriter, *http.Request) { called = true }))
			defer host.Close()

			out := reviewCommand(t, host, item)
			assert.Equal(t, opskat.ReviewError, out.Outcome)
			assert.Equal(t, "not_configured", out.Reason)
			assert.False(t, called)
		})
	}
}

// NaN 阈值不能让高分命令通过：设置错误就是设置错误，不发请求。
func TestReviewNaNThresholdDoesNotPassDangerousCommands(t *testing.T) {
	called := false
	host := opskat.NewTestHost(configured(map[string]any{"threshold": "NaN"}),
		opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) { called = true; jevAnswer(t, w, allQuestions(0.99)) }))
	defer host.Close()

	out := reviewCommand(t, host, item)
	assert.Equal(t, opskat.ReviewError, out.Outcome)
	assert.Equal(t, "not_configured", out.Reason)
	assert.False(t, called)
}

func TestReviewMapsServiceErrors(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        jevreview.ReasonInvalidAPIKey,
		http.StatusInternalServerError: jevreview.ReasonUnavailable,
	}
	for status, reason := range cases {
		t.Run(http.StatusText(status), func(t *testing.T) {
			host := opskat.NewTestHost(configured(nil), opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer host.Close()

			out := reviewCommand(t, host, item)
			assert.Equal(t, opskat.ReviewError, out.Outcome)
			assert.Equal(t, reason, out.Reason)
		})
	}
}

// 扩展里不重试：超时由宿主控制（到时取消这次调用、重试一次）。扩展自己按 Retry-After 等，
// 会一直等到宿主超时，结果是"超时"，而不是很快报"服务不可用"。
func TestReviewDoesNotRetryInsideTheExtension(t *testing.T) {
	calls := 0
	host := opskat.NewTestHost(configured(nil), opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer host.Close()

	start := time.Now()
	out := reviewCommand(t, host, item)
	assert.Equal(t, opskat.ReviewError, out.Outcome)
	assert.Equal(t, jevreview.ReasonUnavailable, out.Reason)
	assert.Equal(t, 1, calls)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// 只按声明的题目判定：兼容服务多回一个没问过的答案，不能算进未通过——
// 宿主会把带未声明题目的结果判成无效。
func TestReviewJudgesOnlyTheDeclaredQuestions(t *testing.T) {
	host := opskat.NewTestHost(configured(nil), opskat.WithMockHTTP(func(w http.ResponseWriter, _ *http.Request) {
		probs := allQuestions(0.01)
		probs["extra"] = 0.99
		jevAnswer(t, w, probs)
	}))
	defer host.Close()

	out := reviewCommand(t, host, item)
	assert.Equal(t, opskat.ReviewPass, out.Outcome)
	assert.Empty(t, out.Failed)
}
