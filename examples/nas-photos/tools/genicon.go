// genicon 生成 nas-photos 的 256x256 icon.png：纯色背景 + 相机形状标记。
// 不依赖第三方库（image/draw 标准库），mkapp.sh 打包时调用：
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

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: go run tools/genicon.go <out.png>")
		os.Exit(1)
	}
	out := os.Args[1]

	bg := color.RGBA{R: 0x1f, G: 0x8a, B: 0x6f, A: 0xff} // 纯色背景（绿）
	fg := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // 相机轮廓（白）

	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			img.Set(x, y, bg)
		}
	}

	drawCamera(img, fg)

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

// drawCamera 画一个极简相机图标：机身矩形 + 镜头圆环 + 顶部取景器凸起。
func drawCamera(img *image.RGBA, fg color.RGBA) {
	bodyX0, bodyY0 := 48, 96
	bodyX1, bodyY1 := 208, 192
	fillRect(img, bodyX0, bodyY0, bodyX1, bodyY1, fg)

	// 取景器凸起（机身上方中央小矩形）。
	fillRect(img, 112, 76, 152, 100, fg)

	// 镜头：用圆环（填充圆减去内部背景色圆）模拟。
	cx, cy := (bodyX0+bodyX1)/2, (bodyY0+bodyY1)/2+4
	fillCircle(img, cx, cy, 44, fg)
	fillCircle(img, cx, cy, 26, color.RGBA{R: 0x1f, G: 0x8a, B: 0x6f, A: 0xff})
	fillCircle(img, cx, cy, 12, fg)
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y, c)
		}
	}
}

func fillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	r2 := r * r
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r2 {
				img.Set(x, y, c)
			}
		}
	}
}
