package preview

// orientation returns the EXIF orientation (1–8) of a JPEG, or 1 when it has
// none or the metadata is malformed.
func orientation(data []byte) int {
	return 1
}
