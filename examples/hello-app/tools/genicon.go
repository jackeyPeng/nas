// genicon 生成 hello-app 的 256x256 icon.png：纯色背景 + "H" 文字标记。
// 不依赖第三方库（image/draw + 内置 5x7 点阵字体），mkapp.sh 打包时调用：
//
//	go run tools/genicon.go <out.png>
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

const iconSize = 256

// glyphH 是字母 "H" 的 5x7 点阵（1=绘制），放大绘制到图标中央。
var glyphH = [7]string{
	"1...1",
	"1...1",
	"1...1",
	"11111",
	"1...1",
	"1...1",
	"1...1",
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: go run tools/genicon.go <out.png>")
		os.Exit(1)
	}
	out := os.Args[1]

	bg := color.RGBA{R: 0x2b, G: 0x6c, B: 0xb0, A: 0xff} // 纯色背景（蓝）
	fg := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // 文字（白）

	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			img.Set(x, y, bg)
		}
	}

	drawGlyphH(img, fg)

	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建文件失败:", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, "编码 PNG 失败:", err)
		os.Exit(1)
	}
	fmt.Println("已生成:", out)
}

// drawGlyphH 把 5x7 点阵字体放大绘制到图标中央。
func drawGlyphH(img *image.RGBA, fg color.RGBA) {
	const scale = 24
	glyphW := 5 * scale
	glyphHeight := 7 * scale
	offsetX := (iconSize - glyphW) / 2
	offsetY := (iconSize - glyphHeight) / 2

	for row, line := range glyphH {
		for col, ch := range line {
			if ch != '1' {
				continue
			}
			x0 := offsetX + col*scale
			y0 := offsetY + row*scale
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.Set(x0+dx, y0+dy, fg)
				}
			}
		}
	}
}
