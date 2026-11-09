package preview

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// ValidateResult checks worker dimensions against the untrusted original header.
func ValidateResult(data []byte, result Result) error {
	width, height, err := expectedSize(data)
	if err != nil {
		return err
	}
	if result.Width != width || result.Height != height || len(result.RGB) != width*height*3 {
		return fmt.Errorf("%w: unexpected dimensions or RGB length", ErrWorker)
	}
	return nil
}

// EncodeJPEG creates a baseline JPEG from validated RGB pixels without metadata.
func EncodeJPEG(result Result) ([]byte, error) {
	if result.Width < 1 || result.Height < 1 || result.Width > MaxSide || result.Height > MaxSide || len(result.RGB) != result.Width*result.Height*3 {
		return nil, fmt.Errorf("%w: invalid RGB", ErrWorker)
	}
	img := image.NewRGBA(image.Rect(0, 0, result.Width, result.Height))
	for i := 0; i < result.Width*result.Height; i++ {
		copy(img.Pix[i*4:i*4+3], result.RGB[i*3:i*3+3])
		img.Pix[i*4+3] = 255
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: Quality}); err != nil {
		return nil, fmt.Errorf("%w: encode JPEG: %v", ErrWorker, err)
	}
	if out.Len() > MaxOutput {
		return nil, fmt.Errorf("%w: JPEG output exceeds budget", ErrWorker)
	}
	return out.Bytes(), nil
}
