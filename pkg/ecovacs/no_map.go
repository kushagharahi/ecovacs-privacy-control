package ecovacs

import (
	"image"
	"image/color"
)

const (
	NoMapPlaceholderWidth  = 320
	NoMapPlaceholderHeight = 220
)

var noMapGlyphs5x7 = map[rune][7]uint8{
	'N': {0x11, 0x19, 0x15, 0x15, 0x13, 0x11, 0x11},
	'O': {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E},
	' ': {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'M': {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11},
	'A': {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11},
	'P': {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10},
}

// RenderNoMapImage generates a clean dark-slate placeholder image displaying "NO MAP".
func RenderNoMapImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, NoMapPlaceholderWidth, NoMapPlaceholderHeight))

	bg := color.RGBA{18, 22, 29, 255}
	border := color.RGBA{45, 53, 66, 255}
	iconStroke := color.RGBA{110, 122, 140, 255}
	slashStroke := color.RGBA{148, 163, 184, 255}
	textCol := color.RGBA{226, 232, 240, 255}

	for y := 0; y < NoMapPlaceholderHeight; y++ {
		for x := 0; x < NoMapPlaceholderWidth; x++ {
			img.SetRGBA(x, y, bg)
		}
	}

	// Subtle inset card border
	const margin = 8
	for x := margin; x < NoMapPlaceholderWidth-margin; x++ {
		img.SetRGBA(x, margin, border)
		img.SetRGBA(x, NoMapPlaceholderHeight-1-margin, border)
	}
	for y := margin; y < NoMapPlaceholderHeight-margin; y++ {
		img.SetRGBA(margin, y, border)
		img.SetRGBA(NoMapPlaceholderWidth-1-margin, y, border)
	}

	// Minimal folded-map-off icon centered above label
	ix0, iy0 := 136, 60
	iw, ih := 48, 34
	for t := 0; t < 2; t++ {
		for x := ix0; x < ix0+iw; x++ {
			img.SetRGBA(x, iy0+t, iconStroke)
			img.SetRGBA(x, iy0+ih-1-t, iconStroke)
		}
		for y := iy0; y < iy0+ih; y++ {
			img.SetRGBA(ix0+t, y, iconStroke)
			img.SetRGBA(ix0+iw-1-t, y, iconStroke)
		}
	}
	for y := iy0 + 2; y < iy0+ih-2; y++ {
		img.SetRGBA(ix0+16, y, border)
		img.SetRGBA(ix0+32, y, border)
	}
	// Diagonal slash across map icon
	for step := -4; step <= iw+4; step++ {
		sx := ix0 - 2 + step
		sy := iy0 - 2 + (step*(ih+4))/(iw+4)
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				px, py := sx+dx, sy+dy
				if px >= 0 && px < NoMapPlaceholderWidth && py >= 0 && py < NoMapPlaceholderHeight {
					img.SetRGBA(px, py, slashStroke)
				}
			}
		}
	}

	// Centered "NO MAP" bitmap label (scale 4 -> 20x28 per glyph, 4px spacing)
	const (
		label   = "NO MAP"
		scale   = 4
		glyphW  = 5 * scale
		spacing = 4
	)
	runes := []rune(label)
	totalW := len(runes)*glyphW + (len(runes)-1)*spacing
	startX := (NoMapPlaceholderWidth - totalW) / 2
	startY := 114

	for idx, ch := range runes {
		rows, ok := noMapGlyphs5x7[ch]
		if !ok {
			continue
		}
		gx := startX + idx*(glyphW+spacing)
		for row := 0; row < 7; row++ {
			bits := rows[row]
			for col := 0; col < 5; col++ {
				if (bits>>(4-col))&1 == 0 {
					continue
				}
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						img.SetRGBA(gx+col*scale+sx, startY+row*scale+sy, textCol)
					}
				}
			}
		}
	}

	return img
}
