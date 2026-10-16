package validator_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/domain"
	"github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestRoomCursor(t *testing.T) {
	id := uuid.NewString()
	position, err := validator.RoomCursor("")
	require.NoError(t, err)
	require.Equal(t, domain.RoomPosition{}, position)
	for _, input := range []string{"0_" + id, "1760000000123456_" + id, "253402300799999999_" + id} {
		position, err := validator.RoomCursor(input)
		require.NoError(t, err)
		require.Equal(t, id, position.ID)
		require.Equal(t, input, validator.FormatRoomCursor(position))
	}
	activity := time.Date(2026, 10, 15, 12, 0, 0, 123456000, time.FixedZone("UTC+3", 3*3600))
	cursor := validator.FormatRoomCursor(domain.RoomPosition{Activity: activity, ID: id})
	require.Equal(t, "1792054800123456_"+id, cursor)
	position, err = validator.RoomCursor(cursor)
	require.NoError(t, err)
	require.True(t, activity.Equal(position.Activity))
	for _, input := range []string{
		"1", "_" + id, "1_", "1_" + id + "_", "01_" + id, "+1_" + id, "-1_" + id, " 1_" + id,
		"1_" + strings.ToUpper(id), "1_urn:uuid:" + id, "1_" + uuid.Nil.String(), "1-" + id,
		"253402300800000000_" + id, "9223372036854775808_" + id,
	} {
		_, err := validator.RoomCursor(input)
		require.ErrorIs(t, err, validator.ErrInvalidRoomCursor, input)
	}
}

func FuzzRoomCursor(f *testing.F) {
	for _, s := range []string{"", "0_d79cba66-65b0-43eb-8985-7dae25ed40e0", "-1_x", "1_D79CBA66-65B0-43EB-8985-7DAE25ED40E0"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		position, err := validator.RoomCursor(input)
		if err != nil || input == "" {
			return
		}
		require.Equal(t, input, validator.FormatRoomCursor(position))
		id, err := validator.CanonicalID(position.ID)
		require.NoError(t, err)
		require.Equal(t, id, position.ID)
		require.GreaterOrEqual(t, position.Activity.UnixMicro(), int64(0))
		require.Less(t, position.Activity.Year(), 10000)
	})
}
