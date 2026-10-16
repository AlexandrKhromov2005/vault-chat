package service

// pageSize applies the default of 50 to a zero limit and rejects limits outside 1–100.
func pageSize(limit int, invalid error) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, invalid
	}
	return limit, nil
}
