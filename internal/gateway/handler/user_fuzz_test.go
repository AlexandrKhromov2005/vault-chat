package handler

import (
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzUserSearchQuery(f *testing.F) {
	f.Add("query=bo&limit=10")
	f.Add("query=bo")
	f.Add("query=a&query=b")
	f.Add("limit=0&query=%zz")
	f.Fuzz(func(t *testing.T, raw string) {
		query, limit, err := userSearchQuery(raw)
		if err != nil {
			return
		}
		require.GreaterOrEqual(t, limit, int32(0))
		require.LessOrEqual(t, limit, int32(50))
		values := url.Values{}
		values.Set("query", query)
		if limit != 0 {
			values.Set("limit", strconv.Itoa(int(limit)))
		}
		gotQuery, gotLimit, err := userSearchQuery(values.Encode())
		require.NoError(t, err)
		require.Equal(t, query, gotQuery)
		require.Equal(t, limit, gotLimit)
	})
}
