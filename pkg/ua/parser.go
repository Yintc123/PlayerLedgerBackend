package ua

import (
	"strings"

	"github.com/mileusna/useragent"
)

// ParseDeviceLabel 从 User-Agent 解析设备标签（§8.9）
// 返回格式："{browser} {os}" 或 "Unknown" 如果无法解析
func ParseDeviceLabel(userAgentStr string) string {
	if userAgentStr == "" {
		return "Unknown"
	}

	ua := useragent.Parse(userAgentStr)

	// 获取浏览器和操作系统信息
	browser := ua.Name
	os := ua.OS

	// 组装标签
	var parts []string
	if browser != "" {
		parts = append(parts, browser)
	}
	if os != "" {
		parts = append(parts, os)
	}

	if len(parts) == 0 {
		return "Unknown"
	}

	return strings.Join(parts, " ")
}
