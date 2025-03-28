package commbadge

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

type CommBadge struct {
	Icon []byte
	img  *image.Image
}

func New(iconBytes []byte) *CommBadge {
	img, err := png.Decode(bytes.NewBuffer(iconBytes))
	if err != nil {
		panic(err)
	}
	return &CommBadge{
		Icon: iconBytes,
		img:  &img,
	}
}

func (cb *CommBadge) SetVolume(volume int) {
	if cb.img == nil {
		return
	}

	img := *cb.img

	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	barRect := image.Rect(0, 0, (w / 5), h)
	bx := barRect.Bounds().Dx()
	bar := getBar(volume, barRect)

	fullRect := image.Rect(0, 0, w+bx, h)

	final := image.NewRGBA(fullRect)

	draw.Draw(final, fullRect, img, image.Point{}, draw.Src)
	draw.Draw(final, barRect.Add(image.Point{X: w}), bar, image.Point{}, draw.Over)

	writer := bytes.NewBuffer(nil)
	png.Encode(writer, final)

	cb.Icon = writer.Bytes()
}

func getBar(volume int, rect image.Rectangle) *image.RGBA {
	h := rect.Dy()
	factor := h / 100
	bx := rect.Bounds().Dx()
	bar := image.NewRGBA(rect)
	for x := 0; x < bx; x++ {
		for y := 0; y < h; y++ {
			if y > (100-volume)*factor {
				bar.Set(x, y, color.RGBA{0, 0, 0, 0xff})
			} else {
				bar.Set(x, y, color.Transparent)
			}
		}
	}

	return bar
}
