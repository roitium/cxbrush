package main

import (
	"fmt"
	"os"

	"cxbrush/internal/assets"
)

// resolveRefFont 返回比对字体来源：CX_REF_FONT > 内嵌字体。
// 返回 path 和 data 二选一。
func resolveRefFont() (path string, data []byte, err error) {
	if v := os.Getenv("CX_REF_FONT"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil, nil
		}
	}
	if len(assets.RefFont) == 0 {
		return "", nil, fmt.Errorf("二进制里没有内嵌比对字体")
	}
	return "", assets.RefFont, nil
}
