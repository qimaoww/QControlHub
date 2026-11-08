package core

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestSiteLogoValidatesDecodedRaster(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {320, 64}, {64, 320}, {2048, 2048}} {
		for _, format := range []string{"png", "jpeg"} {
			var data bytes.Buffer
			picture := image.NewNRGBA(image.Rectangle{Max: size})
			if format == "png" {
				png.Encode(&data, picture)
			} else {
				jpeg.Encode(&data, picture, nil)
			}
			value := "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data.Bytes())
			if err := ValidateSiteLogo(value); err != nil {
				t.Fatalf("%s %v: %v", format, size, err)
			}
			if err := ValidateSiteLogo(value[:len(value)-8]); err == nil {
				t.Fatal("accepted truncated raster")
			}
			wrong := strings.Replace(value, "image/"+format, "image/svg+xml", 1)
			if err := ValidateSiteLogo(wrong); err == nil {
				t.Fatal("accepted SVG MIME")
			}
		}
	}
	var huge bytes.Buffer
	png.Encode(&huge, image.NewNRGBA(image.Rect(0, 0, 4097, 1)))
	for _, value := range []string{"https://example.test/logo.png", "data:image/svg+xml;base64,PHN2Zz4=", "data:image/png;base64,YnJva2Vu", "data:image/png;base64," + base64.StdEncoding.EncodeToString(huge.Bytes()), strings.Repeat("x", 400000)} {
		if ValidateSiteLogo(value) == nil {
			t.Fatal("accepted invalid image")
		}
	}
	if ValidateSiteLogo("") != nil {
		t.Fatal("default rejected")
	}
}
