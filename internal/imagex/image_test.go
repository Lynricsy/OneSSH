package imagex

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestProcessScalesAndPreservesAlpha(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	src.Set(0, 0, color.NRGBA{R: 255, A: 80})
	var in bytes.Buffer
	if err := png.Encode(&in, src); err != nil {
		t.Fatal(err)
	}
	out, err := Process(in.Bytes(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 10 || out.Height != 5 {
		t.Fatalf("尺寸 %dx%d", out.Width, out.Height)
	}
	if out.MIMEType != "image/png" {
		t.Fatalf("类型 %s", out.MIMEType)
	}
	if _, err := png.Decode(bytes.NewReader(out.Data)); err != nil {
		t.Fatal(err)
	}
}
