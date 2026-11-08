package core

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"
)

const MaxSiteLogoBytes = 256 << 10

// SiteBranding is a single public site value, independent of account settings.
type SiteBranding struct {
	Logo      string    `json:"logo"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ValidateSiteLogo(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > base64.StdEncoding.EncodedLen(MaxSiteLogoBytes)+32 {
		return errors.New("Logo 不得超过 256 KiB")
	}
	prefix, encoded, ok := strings.Cut(value, ",")
	if !ok || (prefix != "data:image/png;base64" && prefix != "data:image/jpeg;base64") {
		return errors.New("Logo 仅支持 PNG 或 JPEG 图片")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > MaxSiteLogoBytes {
		return errors.New("Logo 图片无效或超过 256 KiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || prefix != "data:image/"+format+";base64" || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 4<<20 {
		return errors.New("Logo 图片无效，宽高须不超过 4096 像素且总像素不超过 4194304")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return errors.New("Logo 图片内容损坏")
	}
	return nil
}
