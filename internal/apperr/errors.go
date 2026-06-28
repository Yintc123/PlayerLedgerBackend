package apperr

import (
	"errors"
	"fmt"
)

// Domain error sentinel 值
var (
	ErrNotFound           = errors.New("resource not found")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrConflict           = errors.New("conflict")
	ErrInvalidInput       = errors.New("invalid input")
	ErrTokenExpired       = errors.New("token_expired")
	ErrAbsoluteExpired    = errors.New("absolute_expired")
	ErrInvalidToken       = errors.New("invalid_token")
	ErrReplayDetected     = errors.New("replay_detected")
	ErrSessionNotFound    = errors.New("session_not_found")
	ErrSessionRevoked     = errors.New("session_revoked")
	ErrUsernameTaken      = errors.New("username_taken")
	ErrWeakPassword       = errors.New("weak_password")
	ErrInvalidClient      = errors.New("invalid_client")
	ErrTooManyRequests    = errors.New("too many requests")
)

// AppError 应用层错误，包含错误码和细节
type AppError struct {
	Code   string
	Err    error
	Detail string
}

func (ae *AppError) Error() string {
	if ae.Detail != "" {
		return fmt.Sprintf("%s: %s", ae.Code, ae.Detail)
	}
	return ae.Code
}

func (ae *AppError) Unwrap() error {
	return ae.Err
}

// New 创建一个新的应用错误
func New(code string, err error, detail string) *AppError {
	return &AppError{
		Code:   code,
		Err:    err,
		Detail: detail,
	}
}

// Is 检查错误是否为特定类型
func Is(err error, target error) bool {
	return errors.Is(err, target)
}
