package preview

import "encoding/binary"

// orientation returns the EXIF orientation (1–8) of a JPEG, or 1 when it has
// none or the metadata is malformed. Only segments before the image data are
// read, and every offset is checked against the data.
func orientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return 1
		}
		marker := data[i+1]
		if marker == 0xff { // fill byte
			i++
			continue
		}
		if marker == 0xda || marker == 0xd9 { // start of scan, end of image
			return 1
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return 1
		}
		if segment := data[i+4 : i+2+n]; marker == 0xe1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return exifOrientation(segment[6:])
		}
		i += 2 + n
	}
	return 1
}

// exifOrientation reads the Orientation entry of IFD0 in a TIFF structure.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:4]) {
	case "MM\x00\x2a":
		order = binary.BigEndian
	case "II\x2a\x00":
		order = binary.LittleEndian
	default:
		return 1
	}
	offset := uint64(order.Uint32(tiff[4:]))
	if offset < 8 || offset+2 > uint64(len(tiff)) {
		return 1
	}
	start := int(offset)
	count := int(order.Uint16(tiff[start:]))
	for k := 0; k < count; k++ {
		entry := start + 2 + 12*k
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:]) != 0x0112 {
			continue
		}
		// SHORT, one value, stored in the first two bytes of the value field.
		if order.Uint16(tiff[entry+2:]) != 3 || order.Uint32(tiff[entry+4:]) != 1 {
			return 1
		}
		if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}
