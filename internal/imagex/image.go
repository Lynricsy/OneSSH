package imagex

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	_ "image/gif"
)

type Result struct {
	Data           []byte
	MIMEType       string
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
}

func Process(data []byte, maxDim int) (Result, error) {
	if maxDim <= 0 {
		maxDim = 1024
	}
	if maxDim > 2048 {
		maxDim = 2048
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("不是支持的图片（检测类型 %s）: %w", http.DetectContentType(data), err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
		return Result{}, fmt.Errorf("图片像素数过大")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("解码 %s: %w", format, err)
	}
	ow, oh := cfg.Width, cfg.Height
	w, h := ow, oh
	if w > maxDim || h > maxDim {
		if w >= h {
			h = max(1, h*maxDim/w)
			w = maxDim
		} else {
			w = max(1, w*maxDim/h)
			h = maxDim
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	opaque := dst.Opaque()
	mime := "image/png"
	if opaque {
		mime = "image/jpeg"
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80})
	} else {
		err = png.Encode(&out, dst)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Data: out.Bytes(), MIMEType: mime, OriginalWidth: ow, OriginalHeight: oh, Width: w, Height: h}, nil
}
