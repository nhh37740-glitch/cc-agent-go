package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ErrorKind 是前端 JSON code 和结构化日志 error_kind 使用的固定值。
type ErrorKind string

const (
	ErrorInvalidRequest          ErrorKind = "invalid_request"
	ErrorConfig                  ErrorKind = "config_error"
	ErrorProviderAuth            ErrorKind = "provider_auth_failed"
	ErrorProviderRateLimit       ErrorKind = "provider_rate_limited"
	ErrorProvider                ErrorKind = "provider_error"
	ErrorProviderResponseInvalid ErrorKind = "provider_response_invalid"
	ErrorNetwork                 ErrorKind = "network_error"
	ErrorNetworkTimeout          ErrorKind = "network_timeout"
	ErrorStorageRead             ErrorKind = "storage_read_failed"
	ErrorStorageWrite            ErrorKind = "storage_write_failed"
	ErrorTool                    ErrorKind = "tool_error"
	ErrorAgentLimit              ErrorKind = "agent_limit_reached"
	ErrorInternal                ErrorKind = "internal_error"
)

// AppError 保存 Go 服务处理错误响应和日志所需的数据。
type AppError struct {
	Kind           ErrorKind
	Operation      string
	ProviderStatus int
	Err            error
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Operation, e.Kind)
	}
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

// Unwrap 让 errors.As 和 errors.Is 可以继续检查 AppError 内的原始错误。
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewAppError(kind ErrorKind, operation string, providerStatus int, err error) *AppError {
	return &AppError{
		Kind:           kind,
		Operation:      operation,
		ProviderStatus: providerStatus,
		Err:            err,
	}
}

func errorKindOf(err error) ErrorKind {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return ErrorInternal
}

func newNetworkError(operation string, err error) *AppError {
	kind := ErrorNetwork
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		kind = ErrorNetworkTimeout
	}
	return NewAppError(kind, operation, 0, err)
}

func newProviderError(operation string, status int, body []byte, apiKey string) *AppError {
	kind := ErrorProvider
	switch status {
	case 401, 403:
		kind = ErrorProviderAuth
	case 429:
		kind = ErrorProviderRateLimit
	}

	detail := sanitizeProviderDetail(string(body), apiKey)
	if detail == "" {
		detail = "DeepSeek 未返回错误正文"
	}
	return NewAppError(kind, operation, status,
		fmt.Errorf("DeepSeek 返回 HTTP %d: %s", status, detail))
}

var credentialPattern = regexp.MustCompile(`(?i)sk-[a-z0-9_-]{8,}`)

func sanitizeProviderDetail(detail string, apiKey string) string {
	detail = strings.TrimSpace(detail)
	if apiKey != "" {
		detail = strings.ReplaceAll(detail, apiKey, "[REDACTED]")
	}
	detail = credentialPattern.ReplaceAllString(detail, "[REDACTED]")
	runes := []rune(detail)
	if len(runes) > 1000 {
		detail = string(runes[:1000]) + "...[truncated]"
	}
	return detail
}
