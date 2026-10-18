package document

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
)

var (
	red  = color.RGBA{255, 0, 0, 255}
	blue = color.RGBA{0, 0, 255, 255}
)

// splitImage is w x h, red on the left half and blue on the right.
func splitImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if x < w/2 {
				img.SetRGBA(x, y, red)
			} else {
				img.SetRGBA(x, y, blue)
			}
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withOrientation inserts an EXIF APP1 segment with the Orientation tag after the SOI
// marker of a JPEG.
func withOrientation(t *testing.T, data []byte, o uint16, bo binary.ByteOrder) []byte {
	t.Helper()
	var tiff bytes.Buffer
	if bo == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	for _, v := range []any{uint16(42), uint32(8), uint16(1), uint16(0x0112), uint16(3), uint32(1), o, uint16(0), uint32(0)} {
		if err := binary.Write(&tiff, bo, v); err != nil {
			t.Fatal(err)
		}
	}
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	return append(append(data[:2:2], seg...), data[2:]...)
}

func decode(t *testing.T, p Page) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(p.Data))
	if err != nil {
		t.Fatalf("decode %s: %v", p.MediaType, err)
	}
	if "image/"+format != p.MediaType {
		t.Fatalf("data is %s, media type %s", format, p.MediaType)
	}
	return img
}

// near reports whether the pixel at (x, y) is within JPEG noise of want.
func near(img image.Image, x, y int, want color.RGBA) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	d := func(a uint32, w uint8) bool {
		v := int(a >> 8)
		return v-int(w) < 30 && int(w)-v < 30
	}
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

func enabled(maxDim int) ImagePolicy {
	return ImagePolicy{Enabled: true, MaxDimension: maxDim, ConvertTo: "image/jpeg", Quality: 85}
}

func TestImagePolicyString(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		p    ImagePolicy
		want string
	}{
		{ImagePolicy{}, "disabled"},
		{ImagePolicy{MaxDimension: 2048, Quality: 85}, "disabled"},
		{enabled(2048), "enabled;max_dimension=2048;convert_to=image/jpeg;quality=85"},
		{ImagePolicy{Enabled: true}, "enabled;max_dimension=0;convert_to=image/jpeg;quality=85"},
		{ImagePolicy{Enabled: true, MaxDimension: 1024, ConvertTo: "image/png", Quality: 50}, "enabled;max_dimension=1024;convert_to=image/png"},
	} {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.p, got, tt.want)
		}
	}
	got := NewImagePolicy(config.Images{Enabled: true, MaxDimension: 2048, ConvertTo: "image/jpeg", Quality: 85})
	if got != enabled(2048) {
		t.Errorf("NewImagePolicy = %+v", got)
	}
}

func TestVariants(t *testing.T) {
	t.Parallel()
	small := encodePNG(t, splitImage(40, 20))
	big := encodePNG(t, splitImage(120, 60))
	smallJPEG := encodeJPEG(t, splitImage(40, 20))
	tall := encodePNG(t, splitImage(30, 90))
	tests := []struct {
		name      string
		policy    ImagePolicy
		data      []byte
		mediaType string
		wantTypes []string
		wantW     int // of the first variant
		wantH     int
		unchanged bool // the first variant is the input
	}{
		{"small png unchanged", enabled(100), small, "image/png", []string{"image/png"}, 40, 20, true},
		{"small jpeg unchanged", enabled(100), smallJPEG, "image/jpeg", []string{"image/jpeg"}, 40, 20, true},
		{"oversize png downscaled to jpeg", enabled(40), big, "image/png", []string{"image/jpeg", "image/png"}, 40, 20, false},
		{"oversize tall png keeps aspect", enabled(45), tall, "image/png", []string{"image/jpeg", "image/png"}, 15, 45, false},
		{"oversize jpeg downscaled", enabled(60), encodeJPEG(t, splitImage(120, 60)), "image/jpeg", []string{"image/jpeg"}, 60, 30, false},
		{"oversize png to png", ImagePolicy{Enabled: true, MaxDimension: 40, ConvertTo: "image/png"}, big, "image/png", []string{"image/png"}, 40, 20, false},
		{"no limit", enabled(0), big, "image/png", []string{"image/png"}, 120, 60, true},
		{"disabled", ImagePolicy{MaxDimension: 40}, big, "image/png", []string{"image/png"}, 120, 60, true},
		{"heic passes through", enabled(40), []byte("\x00\x00\x00\x18ftypheic"), "image/heic", []string{"image/heic"}, 0, 0, true},
		{"undecodable passes through", enabled(40), pngData, "image/png", []string{"image/png"}, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.policy.variants(context.Background(), tt.data, tt.mediaType)
			if err != nil {
				t.Fatal(err)
			}
			var types []string
			for _, v := range got {
				types = append(types, v.MediaType)
			}
			if strings.Join(types, ",") != strings.Join(tt.wantTypes, ",") {
				t.Fatalf("variant types = %v, want %v", types, tt.wantTypes)
			}
			if tt.unchanged != bytes.Equal(got[0].Data, tt.data) {
				t.Fatalf("first variant unchanged = %v, want %v", !tt.unchanged, tt.unchanged)
			}
			if tt.wantW == 0 || tt.unchanged {
				return
			}
			b := decode(t, got[0]).Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Errorf("size = %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}
		})
	}
}

func TestVariantsTooManyPixels(t *testing.T) {
	t.Parallel()
	// A PNG with only its IHDR chunk, claiming 20000x20000 pixels: DecodeConfig reads
	// no further.
	ihdr := binary.BigEndian.AppendUint32(nil, 20000)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 20000)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	chunk := append([]byte("IHDR"), ihdr...)
	hdr := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	hdr = binary.BigEndian.AppendUint32(append(hdr, chunk...), crc32.ChecksumIEEE(chunk))
	_, err := enabled(2048).variants(context.Background(), hdr, "image/png")
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("err = %v, want payload too large", err)
	}
}

func TestVariantsTransparentToJPEG(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 80)) // fully transparent
	got, err := enabled(40).variants(context.Background(), encodePNG(t, img), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !near(decode(t, got[0]), 5, 5, color.RGBA{255, 255, 255, 255}) {
		t.Error("transparent pixels are not white in the JPEG")
	}
}

func TestEXIFOrientation(t *testing.T) {
	t.Parallel()
	base := encodeJPEG(t, splitImage(40, 20))
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{"none", base, 1},
		{"little endian 6", withOrientation(t, base, 6, binary.LittleEndian), 6},
		{"big endian 8", withOrientation(t, base, 8, binary.BigEndian), 8},
		{"out of range", withOrientation(t, base, 9, binary.BigEndian), 1},
		{"not a jpeg", []byte("nope"), 1},
		{"truncated", withOrientation(t, base, 6, binary.BigEndian)[:12], 1},
	}
	for _, tt := range tests {
		if got := jpegOrientation(tt.data); got != tt.want {
			t.Errorf("%s: orientation = %d, want %d", tt.name, got, tt.want)
		}
	}
	// The segment is bounds-checked against every truncation.
	full := withOrientation(t, base, 6, binary.LittleEndian)
	for n := range 40 {
		jpegOrientation(full[:n])
	}
}

func TestOrientImage(t *testing.T) {
	t.Parallel()
	// A 3x2 image; its top-left pixel is red and its top-right pixel blue.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.SetRGBA(0, 0, red)
	src.SetRGBA(2, 0, blue)
	tests := []struct {
		o      int
		w, h   int
		tl, tr [2]int // where the source's top-left and top-right pixels end up
	}{
		{1, 3, 2, [2]int{0, 0}, [2]int{2, 0}},
		{2, 3, 2, [2]int{2, 0}, [2]int{0, 0}},
		{3, 3, 2, [2]int{2, 1}, [2]int{0, 1}},
		{4, 3, 2, [2]int{0, 1}, [2]int{2, 1}},
		{5, 2, 3, [2]int{0, 0}, [2]int{0, 2}},
		{6, 2, 3, [2]int{1, 0}, [2]int{1, 2}},
		{7, 2, 3, [2]int{1, 2}, [2]int{1, 0}},
		{8, 2, 3, [2]int{0, 2}, [2]int{0, 0}},
	}
	for _, tt := range tests {
		got := orientImage(src, tt.o)
		if got.Rect.Dx() != tt.w || got.Rect.Dy() != tt.h {
			t.Errorf("orientation %d: size %v, want %dx%d", tt.o, got.Rect.Size(), tt.w, tt.h)
			continue
		}
		if got.RGBAAt(tt.tl[0], tt.tl[1]) != red || got.RGBAAt(tt.tr[0], tt.tr[1]) != blue {
			t.Errorf("orientation %d: top-left at %v, top-right at %v", tt.o, tt.tl, tt.tr)
		}
	}
}

func TestVariantsAppliesEXIFOrientation(t *testing.T) {
	t.Parallel()
	// 40x20, red left and blue right, tagged "rotate 90 degrees clockwise" (6): the
	// upright image is 20x40 with red on top.
	data := withOrientation(t, encodeJPEG(t, splitImage(40, 20)), 6, binary.BigEndian)
	got, err := enabled(100).variants(context.Background(), data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MediaType != "image/jpeg" || bytes.Equal(got[0].Data, data) {
		t.Fatalf("variants = %d, first unchanged = %v", len(got), len(got) > 0 && bytes.Equal(got[0].Data, data))
	}
	img := decode(t, got[0])
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("size = %v, want 20x40", b.Size())
	}
	if !near(img, 10, 5, red) || !near(img, 10, 35, blue) {
		t.Error("orientation 6 did not put red on top and blue at the bottom")
	}
	// Disabled: the bytes are untouched, whatever the tag says.
	got, err = ImagePolicy{}.variants(context.Background(), data, "image/jpeg")
	if err != nil || !bytes.Equal(got[0].Data, data) {
		t.Error("disabled policy changed the image")
	}
}

func webpFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/image/split-60x30.webp")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSelectConvertsWebP(t *testing.T) {
	t.Parallel()
	webp := webpFixture(t)
	doc, err := New(Input{MediaType: "image/webp", Data: webp}, config.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var (
		withWebP = []string{"image/webp", "image/png", "image/jpeg", "text/plain"}
		noWebP   = []string{"image/png", "image/jpeg", "text/plain"}
		pngOnly  = []string{"image/png", "text/plain"}
	)
	tests := []struct {
		name       string
		extractor  []string
		judge      []string
		policy     ImagePolicy
		wantType   string
		wantNative bool // the part is the document as given
		wantErr    bool
	}{
		{"jpeg-only groups get a jpeg", noWebP, noWebP, enabled(2048), "image/jpeg", false, false},
		{"webp groups keep the webp", withWebP, withWebP, enabled(2048), "image/webp", true, false},
		{"extractor webp, judge not: both read the jpeg", withWebP, noWebP, enabled(2048), "image/jpeg", false, false},
		{"downscale forces conversion even for webp groups", withWebP, withWebP, enabled(30), "image/jpeg", false, false},
		{"png target", noWebP, noWebP, ImagePolicy{Enabled: true, MaxDimension: 2048, ConvertTo: "image/png"}, "image/png", false, false},
		{"png-only groups with a jpeg target are refused", pngOnly, pngOnly, enabled(2048), "", false, true},
		{"disabled: refused as before", noWebP, noWebP, ImagePolicy{}, "", false, true},
		{"disabled: webp groups unchanged", withWebP, withWebP, ImagePolicy{}, "image/webp", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rep, err := SelectWith(doc, capsFor(t, tt.extractor), capsFor(t, tt.judge), "", tt.policy)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedDocument) {
					t.Fatalf("err = %v, want unsupported document", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if rep.Kind != Native || len(rep.MediaTypes) != 1 || rep.MediaTypes[0] != tt.wantType {
				t.Fatalf("kind %v types %v, want native %s", rep.Kind, rep.MediaTypes, tt.wantType)
			}
			blob := rep.Parts[0].Image
			if blob == nil || blob.MediaType != tt.wantType {
				t.Fatalf("part = %+v", rep.Parts[0])
			}
			if bytes.Equal(blob.Data, webp) != tt.wantNative {
				t.Errorf("part is the original bytes = %v, want %v", !tt.wantNative, tt.wantNative)
			}
			img := decode(t, Page{MediaType: blob.MediaType, Data: blob.Data})
			if tt.wantType == "image/jpeg" || tt.wantType == "image/png" {
				if !near(img, 2, 2, red) || !near(img, img.Bounds().Dx()-3, 2, blue) {
					t.Error("converted pixels differ from the WebP")
				}
			}
		})
	}
}

func TestSelectImageDocuments(t *testing.T) {
	t.Parallel()
	vision := []string{"image/png", "image/jpeg", "text/plain"}
	jpegOnly := []string{"image/jpeg", "text/plain"}
	big := encodePNG(t, splitImage(120, 60))

	doc, err := New(Input{MediaType: "image/png", Data: big}, config.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := SelectWith(doc, capsFor(t, vision), capsFor(t, vision), "", enabled(40))
	if err != nil {
		t.Fatal(err)
	}
	if rep.MediaTypes[0] != "image/jpeg" || rep.Parts[0].Image.MediaType != "image/jpeg" {
		t.Errorf("oversize PNG selected as %v", rep.MediaTypes)
	}
	if b := decode(t, Page{MediaType: "image/jpeg", Data: rep.Parts[0].Image.Data}).Bounds(); b.Dx() != 40 || b.Dy() != 20 {
		t.Errorf("size = %v, want 40x20", b.Size())
	}

	// The same bytes serve both roles: Parts is one slice, built once.
	again, err := SelectWith(doc, capsFor(t, vision), capsFor(t, vision), "", enabled(40))
	if err != nil || !bytes.Equal(again.Parts[0].Image.Data, rep.Parts[0].Image.Data) {
		t.Error("conversion is not deterministic")
	}

	// A group that reads only PNG still gets the original oversize PNG, as before.
	rep, err = SelectWith(doc, capsFor(t, []string{"image/png"}), capsFor(t, []string{"image/png"}), "", enabled(40))
	if err != nil || rep.MediaTypes[0] != "image/png" || !bytes.Equal(rep.Parts[0].Image.Data, big) {
		t.Errorf("PNG-only fallback = %v, %v", rep.MediaTypes, err)
	}

	// A small PNG is not re-encoded.
	small := encodePNG(t, splitImage(40, 20))
	doc, err = New(Input{MediaType: "image/png", Data: small}, config.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err = SelectWith(doc, capsFor(t, vision), capsFor(t, vision), "", enabled(100))
	if err != nil || rep.MediaTypes[0] != "image/png" || !bytes.Equal(rep.Parts[0].Image.Data, small) {
		t.Errorf("small PNG = %v, %v", rep.MediaTypes, err)
	}
	// Select (no image policy) is the old behaviour.
	rep, err = Select(doc, capsFor(t, jpegOnly), capsFor(t, jpegOnly), "")
	if !errors.Is(err, ErrUnsupportedDocument) {
		t.Errorf("Select with a JPEG-only group = %v, %v", rep.MediaTypes, err)
	}
}

func TestSelectConvertsPages(t *testing.T) {
	t.Parallel()
	vision := []string{"image/png", "image/jpeg", "text/plain"}
	pngOnly := []string{"image/png", "text/plain"}
	big := encodePNG(t, splitImage(120, 60))
	small := encodeJPEG(t, splitImage(40, 20))
	doc, err := New(Input{
		MediaType: "application/pdf",
		Data:      pdfData,
		Pages:     []Page{{Data: big}, {Data: small}},
	}, config.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	// The PDF is not readable, so the pages are: the oversize one is downscaled, the
	// small one stays as it is.
	rep, err := SelectWith(doc, capsFor(t, vision), capsFor(t, vision), "", enabled(40))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != Pages || len(rep.Parts) != 2 {
		t.Fatalf("kind %v, %d parts", rep.Kind, len(rep.Parts))
	}
	if rep.Parts[0].Image.MediaType != "image/jpeg" || bytes.Equal(rep.Parts[0].Image.Data, big) {
		t.Error("page 1 was not converted")
	}
	if !bytes.Equal(rep.Parts[1].Image.Data, small) {
		t.Error("page 2 was re-encoded")
	}
	if len(rep.MediaTypes) != 1 || rep.MediaTypes[0] != "image/jpeg" {
		t.Errorf("types = %v", rep.MediaTypes)
	}
	// PNG-only groups cannot read the converted JPEG pages: the originals are used.
	pngPages, err := New(Input{MediaType: "application/pdf", Data: pdfData, Pages: []Page{{Data: big}, {Data: big}}}, config.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err = SelectWith(pngPages, capsFor(t, pngOnly), capsFor(t, pngOnly), "", enabled(40))
	if err != nil || rep.Kind != Pages || rep.MediaTypes[0] != "image/png" || !bytes.Equal(rep.Parts[0].Image.Data, big) {
		t.Fatalf("PNG-only pages: %v %v, %v", rep.Kind, rep.MediaTypes, err)
	}
}

// TestConvertWaitsForDecodeSlot: with every decode slot taken, a conversion waits, and
// proceeds once a slot is free, so the decoded images in memory are bounded per process.
// Not parallel: it holds the process-wide slots.
func TestConvertWaitsForDecodeSlot(t *testing.T) {
	for range maxConcurrentDecodes {
		decodeSlots <- struct{}{}
	}
	released := false
	release := func() {
		if !released {
			released = true
			for range maxConcurrentDecodes {
				<-decodeSlots
			}
		}
	}
	t.Cleanup(release)
	data := encodePNG(t, splitImage(80, 40))
	done := make(chan error, 1)
	go func() {
		_, err := enabled(40).resolve().convert(context.Background(), data)
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("convert ran with every decode slot taken")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("convert did not proceed after the slots were freed")
	}
}

// TestConvertDecodeSlotWaitEndsWithContext: with every decode slot taken, a conversion
// whose context ends stops waiting and returns the context's error, instead of offering
// the image as given or holding the request. Not parallel: it holds the process-wide
// slots.
func TestConvertDecodeSlotWaitEndsWithContext(t *testing.T) {
	for range maxConcurrentDecodes {
		decodeSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range maxConcurrentDecodes {
			<-decodeSlots
		}
	})
	data := encodePNG(t, splitImage(80, 40)) // over max_dimension 40: needs conversion
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	forms, err := enabled(40).variants(ctx, data, "image/png")
	if !errors.Is(err, context.DeadlineExceeded) || forms != nil {
		t.Errorf("variants = %d forms, %v; want context.DeadlineExceeded", len(forms), err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("variants waited %v after its context ended", d)
	}
}
