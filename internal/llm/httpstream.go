package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RetryBaseDelay 是指数退避的基准间隔。测试可下调以加速。
var RetryBaseDelay = 500 * time.Millisecond

// maxAttempts 是建连阶段的重试上限（含首次）。
const maxAttempts = 3

// PostStream 发起流式请求，并在「尚未产出任何事件」阶段做指数退避重试：
// 建连失败 / 429 / 5xx 重试，封顶 maxAttempts 次；一旦拿到 2xx（流即将开始）
// 便返回响应交给上层读 SSE——之后的错误由 Stream.Next 直接返回，不在此续流。
//
// buildReq 每次尝试重新构造请求（body 已被上一次消费，需可重建）。
func PostStream(ctx context.Context, client *http.Client, buildReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := RetryBaseDelay << (attempt - 1)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		req, err := buildReq()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue // 建连失败 → 重试
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("llm: http %d: %s", resp.StatusCode, readErrBody(resp))
			resp.Body.Close()
			continue // 429/5xx → 重试
		}
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("llm: http %d: %s", resp.StatusCode, readErrBody(resp))
			resp.Body.Close()
			return nil, err // 其他非 2xx 不重试
		}
		return resp, nil
	}
	return nil, fmt.Errorf("llm: request failed after %d attempts: %w", maxAttempts, lastErr)
}

func readErrBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	return string(b)
}
