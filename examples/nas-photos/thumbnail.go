package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
)

// thumbMaxEdge 是缩略图最长边（任务 §4「最长边 320px」）。
const thumbMaxEdge = 320

// errCorruptImage 标记源文件解码失败（损坏图片），handlePhoto/handleThumb
// 据此返回 404 占位而不是 500（任务 §4「损坏图→404 占位」）。
var errCorruptImage = errors.New("图片解码失败")

// decodeAny 按扩展名选择对应标准库解码器（jpeg/png/gif），不用
// image.Decode 的格式自动探测——任务白名单已限定三种格式，显式分发更明确，
// 也避免信任文件内容里的格式魔数做出超出白名单的行为。
func decodeAny(r io.Reader, ext string) (image.Image, error) {
	var img image.Image
	var err error
	switch ext {
	case "jpg", "jpeg":
		img, err = jpeg.Decode(r)
	case "png":
		img, err = png.Decode(r)
	case "gif":
		img, err = gif.Decode(r)
	default:
		return nil, fmt.Errorf("不支持的扩展名: %s", ext)
	}
	if err != nil {
		return nil, errCorruptImage
	}
	return img, nil
}

// makeThumbnail 把 src 缩放到最长边 thumbMaxEdge 以内，原图小于阈值时不放大
// （缩略图不应比原图还大）。标准库 image/draw 不含任意比例缩放器（双线性
// 缩放器 draw.ApproxBiLinear 在 golang.org/x/image/draw，属第三方模块，
// 本应用零依赖硬边界下不可用），这里按任务要求的效果（不用最近邻，用
// 双线性插值）自实现 bilinearScale。
func makeThumbnail(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}

	dw, dh := w, h
	if w > thumbMaxEdge || h > thumbMaxEdge {
		if w >= h {
			dw = thumbMaxEdge
			dh = h * thumbMaxEdge / w
		} else {
			dh = thumbMaxEdge
			dw = w * thumbMaxEdge / h
		}
		if dw < 1 {
			dw = 1
		}
		if dh < 1 {
			dh = 1
		}
	}

	return bilinearScale(src, dw, dh)
}

// bilinearScale 对 src 做双线性插值缩放，输出 dw x dh 的 RGBA 图像。对每个
// 目标像素反向映射到源图坐标，取周围 2x2 像素按距离加权平均——比
// NearestNeighbor 平滑，是任务要求的「不用最近邻」的标准库可行实现。
func bilinearScale(src image.Image, dw, dh int) *image.RGBA {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))

	if sw <= 1 || sh <= 1 || dw <= 0 || dh <= 0 {
		// 退化情况（1px 宽/高源图）：直接采样最近点，避免除零。
		for y := 0; y < dh; y++ {
			for x := 0; x < dw; x++ {
				dst.Set(x, y, src.At(bounds.Min.X, bounds.Min.Y))
			}
		}
		return dst
	}

	scaleX := float64(sw) / float64(dw)
	scaleY := float64(sh) / float64(dh)

	for y := 0; y < dh; y++ {
		srcYf := (float64(y)+0.5)*scaleY - 0.5
		y0 := int(srcYf)
		yFrac := srcYf - float64(y0)
		y1 := y0 + 1
		y0 = clampInt(y0, 0, sh-1)
		y1 = clampInt(y1, 0, sh-1)

		for x := 0; x < dw; x++ {
			srcXf := (float64(x)+0.5)*scaleX - 0.5
			x0 := int(srcXf)
			xFrac := srcXf - float64(x0)
			x1 := x0 + 1
			x0 = clampInt(x0, 0, sw-1)
			x1 = clampInt(x1, 0, sw-1)

			c00 := src.At(bounds.Min.X+x0, bounds.Min.Y+y0)
			c10 := src.At(bounds.Min.X+x1, bounds.Min.Y+y0)
			c01 := src.At(bounds.Min.X+x0, bounds.Min.Y+y1)
			c11 := src.At(bounds.Min.X+x1, bounds.Min.Y+y1)

			dst.Set(x, y, bilerp(c00, c10, c01, c11, xFrac, yFrac))
		}
	}
	return dst
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// bilerp 对四个角的颜色按 (xFrac, yFrac) 双线性插值，分量在 16 位空间算
// （image/color 的 RGBA() 返回 16 位分量），结果转回 8 位 RGBA。
func bilerp(c00, c10, c01, c11 color.Color, xFrac, yFrac float64) color.RGBA {
	r00, g00, b00, a00 := c00.RGBA()
	r10, g10, b10, a10 := c10.RGBA()
	r01, g01, b01, a01 := c01.RGBA()
	r11, g11, b11, a11 := c11.RGBA()

	lerp := func(a, b uint32, f float64) float64 {
		return float64(a)*(1-f) + float64(b)*f
	}

	topR := lerp(uint32(r00), uint32(r10), xFrac)
	botR := lerp(uint32(r01), uint32(r11), xFrac)
	r := lerp(uint32(topR), uint32(botR), yFrac)

	topG := lerp(uint32(g00), uint32(g10), xFrac)
	botG := lerp(uint32(g01), uint32(g11), xFrac)
	g := lerp(uint32(topG), uint32(botG), yFrac)

	topB := lerp(uint32(b00), uint32(b10), xFrac)
	botB := lerp(uint32(b01), uint32(b11), xFrac)
	b := lerp(uint32(topB), uint32(botB), yFrac)

	topA := lerp(uint32(a00), uint32(a10), xFrac)
	botA := lerp(uint32(a01), uint32(a11), xFrac)
	a := lerp(uint32(topA), uint32(botA), yFrac)

	return color.RGBA{
		R: uint8(uint32(r) >> 8),
		G: uint8(uint32(g) >> 8),
		B: uint8(uint32(b) >> 8),
		A: uint8(uint32(a) >> 8),
	}
}

// genThumbnail 读取原图文件、解码、生成缩略图并编码为 JPEG 写入 w。
func genThumbnail(originalPath, ext string, w io.Writer) error {
	f, err := os.Open(originalPath)
	if err != nil {
		return err
	}
	defer f.Close()

	src, err := decodeAny(f, ext)
	if err != nil {
		return err
	}
	thumb := makeThumbnail(src)
	return jpeg.Encode(w, thumb, &jpeg.Options{Quality: 82})
}
