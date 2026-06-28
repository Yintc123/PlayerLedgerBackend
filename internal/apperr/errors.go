package apperr

import (
	"errors"
	"fmt"
)

// Domain error sentinel 值（§12.2）
var (
	ErrNotFound        = errors.New("resource not found")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrConflict        = errors.New("resource already exists")
	ErrInvalidInput    = errors.New("invalid input")
	ErrTokenExpired    = errors.New("token expired")
	ErrAbsoluteExpired = errors.New("absolute expired")
	ErrInvalidToken    = errors.New("invalid token")
	ErrReplayDetected  = errors.New("replay detected")
	ErrFamilyNotFound  = errors.New("session not found") // family 不存在（已過期 / 已被廢）
	ErrSessionRevoked  = errors.New("session revoked")
	ErrUsernameTaken   = errors.New("username taken")
	ErrWeakPassword    = errors.New("weak password")
	ErrInvalidClient   = errors.New("invalid client")
	ErrTooManyRequests = errors.New("too many requests")
)

// AppError 應用層錯誤，包含錯誤碼和細節
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

// New 建立新的應用錯誤
func New(code string, err error, detail string) *AppError {
	return &AppError{
		Code:   code,
		Err:    err,
		Detail: detail,
	}
}

// Is 檢查錯誤是否為特定類型
func Is(err error, target error) bool {
	return errors.Is(err, target)
}
