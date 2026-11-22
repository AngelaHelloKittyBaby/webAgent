package llm

import (
	"fmt"
	"time"
)

// APIError 是所有模型接口错误的统一类型。
// StatusCode == 0 表示网络层就失败了（没拿到响应）。
type APIError struct {
	StatusCode int
	Body       string        // 响应体片段，最多 500 字符，用于排查
	Retryable  bool          // 这个错误重试有意义吗
	RetryAfter time.Duration // 服务端明确要求的等待时间（429 常见）
}

func (e *APIError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("network error: %s", e.Body)
	}
	return fmt.Sprintf("http error: %d, %s", e.StatusCode, e.Body)
}
