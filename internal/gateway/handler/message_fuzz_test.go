package handler

import (
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzHistoryQuery(f *testing.F) {
	f.Add("cursor=5&limit=10")
	f.Add("limit=0")
	f.Add("cursor=%zz")
	f.Fuzz(func(t *testing.T, raw string) {
		cursor, limit, err := historyQuery(raw)
		if err != nil {
			return
		}
		require.GreaterOrEqual(t, limit, int32(0))
		require.LessOrEqual(t, limit, int32(100))
		values := url.Values{}
		values.Set("cursor", cursor)
		if limit != 0 {
			values.Set("limit", strconv.Itoa(int(limit)))
		}
		gotCursor, gotLimit, err := historyQuery(values.Encode())
		require.NoError(t, err)
		require.Equal(t, cursor, gotCursor)
		require.Equal(t, limit, gotLimit)
	})
}
