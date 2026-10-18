package document

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"

	"github.com/kaanemec/vde/internal/config"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder with image.Decode
)

// maxPixels bounds the decoded size of one image: 120 megapixels is about 480 MB as
// RGBA. A compressed image can be tiny and still decode to far more.
const maxPixels = 120_000_000

// maxConcurrentDecodes bounds the images the process decodes at once. One decode at
// maxPixels holds up to about 1.2 GB (the decoded source, its RGBA copy and a rotated
// copy), so this, not the number of concurrent requests and jobs, bounds the memory
// image conversion takes. A conversion waits for a slot; each holds it for seconds.
const maxConcurrentDecodes = 2

var decodeSlots = make(chan struct{}, maxConcurrentDecodes)

// Defaults for ImagePolicy fields left at zero.
const (
	defaultConvertTo = "image/jpeg"
	defaultQuality   = 85
)

// ImagePolicy is how images (an image document and a caller's page images) are prepared
// once, so that the extractor and the judge read the same bytes (ADR 0006). It is part of
// service_version, through String. The zero value is disabled: images are used as given.
//
// When enabled, an image that is larger than MaxDimension on its longer side is scaled
// down, a JPEG with an EXIF orientation is rotated upright, and either is re-encoded as
// ConvertTo. A PNG or JPEG that needs neither is never re-encoded. WebP is decoded and
// converted to ConvertTo when no endpoint of a group reads WebP (see SelectWith).
// HEIC has no pure-Go decoder (ADR 0001, no cgo), so it is passed through, and only
// endpoints that declare image/heic read it.
type ImagePolicy struct {
	Enabled      bool
	MaxDimension int    // pixels on the longer side; zero or less means no downscaling
	ConvertTo    string // image/jpeg (the default) or image/png
	Quality      int    // JPEG quality 1-100; zero means 85
}

// NewImagePolicy returns the policy of a configuration.
func NewImagePolicy(c config.Images) ImagePolicy {
	return ImagePolicy{Enabled: c.Enabled, MaxDimension: c.MaxDimension, ConvertTo: c.ConvertTo, Quality: c.Quality}
}

func (p ImagePolicy) resolve() ImagePolicy {
	if p.ConvertTo == "" {
		p.ConvertTo = defaultConvertTo
	}
	if p.Quality <= 0 || p.Quality > 100 {
		p.Quality = defaultQuality
	}
	return p
}

// String is the canonical form hashed into service_version: "disabled", or the enabled
// settings with defaults filled in. The quality is left out when the target is PNG.
func (p ImagePolicy) String() string {
	if !p.Enabled {
		return "disabled"
	}
	p = p.resolve()
	s := fmt.Sprintf("enabled;max_dimension=%d;convert_to=%s", max(p.MaxDimension, 0), p.ConvertTo)
	if p.ConvertTo == "image/jpeg" {
		s += fmt.Sprintf(";quality=%d", p.Quality)
	}
	return s
}

// imageInfo is what is read from an image without decoding its pixels.
type imageInfo struct {
	w, h   int
	orient int // EXIF orientation 1-8; 1 for anything but a JPEG with a valid tag
}

func inspectImage(data []byte, mediaType string) (imageInfo, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return imageInfo{}, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return imageInfo{}, errors.New("empty image")
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return imageInfo{}, refuse(CodePayloadTooLarge, "image is %dx%d pixels, over the limit of %d megapixels",
			cfg.Width, cfg.Height, maxPixels/1_000_000)
	}
	info := imageInfo{w: cfg.Width, h: cfg.Height, orient: 1}
	if mediaType == "image/jpeg" {
		info.orient = jpegOrientation(data)
	}
	return info, nil
}

func (p ImagePolicy) needsWork(i imageInfo) bool {
	return i.orient > 1 || (p.MaxDimension > 0 && max(i.w, i.h) > p.MaxDimension)
}

// variants returns the forms of one image in order of preference, the last being the
// image as given when conversion is not what the endpoints read. A PNG or JPEG that
// needs no work is returned as it is; a WebP additionally offers its conversion. An
// image that cannot be decoded is returned as it is (the models decide), except when it
// exceeds maxPixels, which is refused.
func (p ImagePolicy) variants(data []byte, mediaType string) ([]Page, error) {
	orig := Page{MediaType: mediaType, Data: data}
	if !p.Enabled || mediaType == "image/heic" {
		return []Page{orig}, nil
	}
	p = p.resolve()
	info, err := inspectImage(data, mediaType)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return nil, err
		}
		return []Page{orig}, nil
	}
	work := p.needsWork(info)
	if !work && mediaType != "image/webp" {
		return []Page{orig}, nil
	}
	out, err := p.convert(data)
	if err != nil {
		return []Page{orig}, nil //nolint:nilerr // a failed conversion offers the image as given, not an error
	}
	conv := Page{MediaType: p.ConvertTo, Data: out}
	switch {
	case conv.MediaType == orig.MediaType && !work:
		return []Page{orig}, nil
	case !work:
		return []Page{orig, conv}, nil
	case conv.MediaType == orig.MediaType:
		return []Page{conv}, nil
	}
	return []Page{conv, orig}, nil
}

// convert decodes the image, scales it down to MaxDimension, applies the EXIF
// orientation and encodes it as ConvertTo, holding one of the decode slots. p must be
// resolved.
func (p ImagePolicy) convert(data []byte) ([]byte, error) {
	decodeSlots <- struct{}{}
	defer func() { <-decodeSlots }()
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	orient := 1
	if format == "jpeg" {
		orient = jpegOrientation(data)
	}
	rgba := toRGBA(scaleDown(src, p.MaxDimension))
	rgba = orientImage(rgba, orient)
	var buf bytes.Buffer
	switch p.ConvertTo {
	case "image/png":
		err = png.Encode(&buf, rgba)
	default:
		if !rgba.Opaque() {
			white := image.NewRGBA(rgba.Bounds())
			draw.Draw(white, white.Bounds(), image.White, image.Point{}, draw.Src)
			draw.Draw(white, white.Bounds(), rgba, rgba.Bounds().Min, draw.Over)
			rgba = white
		}
		err = jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: p.Quality})
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scaleDown shrinks src so its longer side is at most limit, keeping the aspect ratio;
// it returns src when it already fits or limit is not positive.
func scaleDown(src image.Image, limit int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if limit <= 0 || (w <= limit && h <= limit) {
		return src
	}
	nw, nh := limit, limit
	if w >= h {
		nh = max(1, int((int64(h)*int64(limit)+int64(w)/2)/int64(w)))
	} else {
		nw = max(1, int((int64(w)*int64(limit)+int64(h)/2)/int64(h)))
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

// toRGBA returns img as an *image.RGBA with its origin at (0, 0), without copying when
// it already is one.
func toRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	if r, ok := img.(*image.RGBA); ok && b.Min == (image.Point{}) {
		return r
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// orientImage returns src turned upright for EXIF orientation o (2-8; anything else
// returns src). The source's top-left pixel ends up at: 2 top-right, 3 bottom-right,
// 4 bottom-left, 5 top-left (transpose), 6 top-right (rotated 90 degrees clockwise),
// 7 bottom-right (transverse), 8 bottom-left (rotated 90 degrees counterclockwise).
func orientImage(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

// jpegOrientation returns the EXIF orientation (1-8) of a JPEG, or 1 when it has none or
// the tag is malformed. It reads only the APP1 segment; the rest of the EXIF block and
// the image data are not parsed.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		m := data[i+1]
		switch {
		case m == 0xFF: // fill byte
			i++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8): // markers without a length
			i += 2
			continue
		case m == 0xDA || m == 0xD9: // image data or end: APP1 comes before it
			return 1
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return 1
		}
		if m == 0xE1 {
			if o := exifOrientation(data[i+4 : i+2+n]); o != 0 {
				return o
			}
		}
		i += 2 + n
	}
	return 1
}

// exifOrientation reads the Orientation tag (0x0112) of IFD0 from an APP1 payload, or
// returns 0 when it is not an EXIF segment or has no valid orientation.
func exifOrientation(seg []byte) int {
	if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
		return 0
	}
	t := seg[6:]
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(t[2:]) != 42 {
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off > len(t)-2 {
		return 0
	}
	n := int(bo.Uint16(t[off:]))
	for k := range n {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) != 0x0112 {
			continue
		}
		if bo.Uint16(t[e+2:]) != 3 || bo.Uint32(t[e+4:]) != 1 { // one SHORT
			return 0
		}
		if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 0
	}
	return 0
}
