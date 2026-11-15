package files

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// PreviewSize is the long side of an image preview. It is what models see (about 2000 image tokens at most for
// Qwen-style vision) and what the chat shows.
const PreviewSize = 1536

// maxPixels: larger images are stored but not decoded (a decompression bomb would need gigabytes).
const maxPixels = 60_000_000

func imageFile(mime string, data []byte) Extracted {
	x := Extracted{Mime: mime, Kind: "image"}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		x.Note = "the image could not be read: " + err.Error()
		return x
	}
	if cfg.Width*cfg.Height > maxPixels {
		x.Width, x.Height = cfg.Width, cfg.Height
		x.Note = "the image is too large to preview"
		return x
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		x.Note = "the image could not be read: " + err.Error()
		return x
	}
	o := 1
	if mime == "image/jpeg" {
		o = exifOrientation(data)
	}
	b := img.Bounds()
	x.Width, x.Height = b.Dx(), b.Dy()
	if o >= 5 { // turned a quarter: the upright image is the other way round
		x.Width, x.Height = x.Height, x.Width
	}
	w, h := b.Dx(), b.Dy()
	if w > PreviewSize || h > PreviewSize {
		if w >= h {
			w, h = PreviewSize, max(1, h*PreviewSize/w)
		} else {
			w, h = max(1, w*PreviewSize/h), PreviewSize
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src) // transparency on white
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	// turned upright after scaling, so a full-size photo is never turned pixel by pixel
	up := orient(dst, o)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, up, &jpeg.Options{Quality: 85}); err == nil {
		x.Preview = out.Bytes()
	}
	return x
}

// exifOrientation reads the EXIF orientation of a JPEG (1 = as stored). Phones store photos sideways and set this.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if marker == 0xDA || size < 2 || i+2+size > len(b) { // start of scan: no EXIF before the image data
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 { // Orientation, a SHORT
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient turns an image upright for an EXIF orientation (2 to 8; anything else is returned as is).
func orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2: // mirrored
				nx, ny = w-1-x, y
			case 3: // upside down
				nx, ny = w-1-x, h-1-y
			case 4: // mirrored upside down
				nx, ny = x, h-1-y
			case 5: // mirrored, turned left
				nx, ny = y, x
			case 6: // turned left: turn right
				nx, ny = h-1-y, x
			case 7: // mirrored, turned right
				nx, ny = h-1-y, w-1-x
			case 8: // turned right: turn left
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
