package preview

import (
	"encoding/binary"
	"testing"
)

func FuzzWorkerResponse(f *testing.F) {
	valid := make([]byte, 8+6)
	binary.BigEndian.PutUint32(valid, 2)
	binary.BigEndian.PutUint32(valid[4:], 1)
	f.Add(valid)
	f.Add([]byte(nil))
	f.Add([]byte{0, 0, 0, 1})
	f.Add(append(append([]byte{}, valid...), 0))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := parseWorkerResponse(data, 2, 1)
		if err != nil {
			return
		}
		if got.Width != 2 || got.Height != 1 || len(got.RGB) != 6 {
			t.Fatalf("accepted response %dx%d with %d RGB bytes", got.Width, got.Height, len(got.RGB))
		}
	})
}
