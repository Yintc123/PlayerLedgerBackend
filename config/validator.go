package config

import "github.com/go-playground/validator/v10"

// NewValidator 创建一个新的 validator 实例。
func NewValidator() *validator.Validate {
	return validator.New()
}
