package preview

import "encoding/binary"

// MaxDecoded bounds, in bytes, what the standard decoders and shrink
// allocate for one image, as estimated from its header by decodedBytes.
// It admits a 24-megapixel 8-bit RGBA PNG; heavier encodings of the same
// size are skipped.
const MaxDecoded = 100 << 20

// decodedBytes estimates from the header, before decoding, how much memory
// decoding data takes. It follows the allocations of image/png and
// image/jpeg and errs on the high side; ok is false for an unreadable
// header. data has passed image.DecodeConfig as format.
func decodedBytes(data []byte, format string, w, h int) (n int64, ok bool) {
	W, H := int64(w), int64(h)
	if format == "png" {
		if len(data) < 29 {
			return 0, false
		}
		depth, interlaced := data[24], data[28] == 1
		px := int64(4) // Gray, Paletted, RGB and RGBA up to 8 bits decode to ≤ 4 bytes
		if depth == 16 {
			px = 8
		}
		n = W * H * px
		if interlaced {
			n *= 2 // each pass is decoded into an image of its own first
		}
		// Two filter rows in the decoder and one RGB row in shrink.
		return n + W*(2*px+3), true
	}
	// Frame payload: precision, height, width, component count, then
	// (id, sampling, table) per component.
	marker, sof := jpegFrame(data)
	if len(sof) < 6 || 6+int(sof[5])*3 > len(sof) {
		return 0, false
	}
	comps := sof[6 : 6+int(sof[5])*3]
	hmax, vmax := int64(1), int64(1)
	for i := 1; i < len(comps); i += 3 {
		hmax, vmax = max(hmax, int64(comps[i]>>4)), max(vmax, int64(comps[i]&15))
	}
	mcuW, mcuH := (W+8*hmax-1)/(8*hmax), (H+8*vmax-1)/(8*vmax)
	// Bytes per padded pixel: Gray; YCbCr counted as 4:4:4, the layout of
	// unusual subsamplings; CMYK keeps YCbCr and black next to the result.
	per := int64(8)
	switch len(comps) / 3 {
	case 1:
		per = 1
	case 3:
		per = 3
	}
	n = per * mcuW * 8 * hmax * mcuH * 8 * vmax
	if marker == 0xc2 {
		// Progressive: every component keeps its coefficients as int32.
		for i := 1; i < len(comps); i += 3 {
			n += 4 * mcuW * 8 * int64(comps[i]>>4) * mcuH * 8 * int64(comps[i]&15)
		}
	}
	return n + 3*W, true
}

// jpegFrame returns the marker and payload of the first SOF0, SOF1 or SOF2
// segment, the frames image/jpeg decodes, or a nil payload.
func jpegFrame(data []byte) (byte, []byte) {
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 0, nil
		}
		marker := data[i+1]
		switch {
		case marker == 0xff: // fill byte
			i++
			continue
		case marker == 0x01 || marker >= 0xd0 && marker <= 0xd8:
			i += 2 // standalone marker
			continue
		}
		end := i + 2 + int(binary.BigEndian.Uint16(data[i+2:]))
		if end < i+4 || end > len(data) {
			return 0, nil
		}
		if marker >= 0xc0 && marker <= 0xc2 {
			return marker, data[i+4 : end]
		}
		i = end
	}
	return 0, nil
}
