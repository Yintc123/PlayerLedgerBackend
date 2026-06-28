package config

import "github.com/go-playground/validator/v10"

// NewValidator 建立一个新的 validator 實例。
func NewValidator() *validator.Validate {
	return validator.New()
}
