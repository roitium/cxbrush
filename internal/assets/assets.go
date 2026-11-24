// Package assets 存放编译期要打进二进制的静态资源。
package assets

import _ "embed"

// RefFont 是字体还原用的比对字体：Noto Sans CJK SC DemiLight（SIL OFL 1.1）。
//
//go:embed NotoSansCJKsc-DemiLight.otf
var RefFont []byte
