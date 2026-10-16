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
	f.Fuzz(func(t *testing.T, raw string) { requireCanonicalPage(t, historyQuery, raw) })
}

func FuzzRoomsQuery(f *testing.F) {
	f.Add("cursor=1792054800123456_d79cba66-65b0-43eb-8985-7dae25ed40e0&limit=10")
	f.Add("cursor=5")
	f.Add("limit=100&limit=1")
	f.Fuzz(func(t *testing.T, raw string) { requireCanonicalPage(t, roomsQuery, raw) })
}

// requireCanonicalPage checks that accepted queries re-encode to the same page.
func requireCanonicalPage(t *testing.T, parse func(string) (string, int32, error), raw string) {
	t.Helper()
	cursor, limit, err := parse(raw)
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
	gotCursor, gotLimit, err := parse(values.Encode())
	require.NoError(t, err)
	require.Equal(t, cursor, gotCursor)
	require.Equal(t, limit, gotLimit)
}
