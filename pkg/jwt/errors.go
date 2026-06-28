// Package jwt — sentinel errors。
//
// pkg/jwt 是 reusable infrastructure package（§2.1 禁止 import internal/*），
// 因此不能直接回 internal/apperr 的 domain error。改用本檔自有 sentinel，
// 由 service 層用 errors.Is 轉譯為 apperr.ErrXxx（與 §8.3.2 hasher.ErrMismatch
// → apperr.ErrUnauthorized 的 pattern 對稱）。
package jwt

import "errors"

var (
	// ErrTokenExpired — exp claim 已過期（含 ClockSkewLeeway 容忍後仍過期）
	ErrTokenExpired = errors.New("jwt: token expired")

	// ErrAbsoluteExpired — refresh token 的 abs_exp 已過期（rotation 不延長 family 絕對壽命）
	ErrAbsoluteExpired = errors.New("jwt: absolute expired")

	// ErrInvalidToken — 簽名錯誤、format 錯誤、iss/aud 不符、claim 缺漏等
	ErrInvalidToken = errors.New("jwt: invalid token")

	// ErrInvalidClient — client_id 未註冊於 ClientPolicies（login / refresh 用）
	ErrInvalidClient = errors.New("jwt: invalid client")
)
