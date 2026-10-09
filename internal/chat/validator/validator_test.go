package validator_test

import (
 "strings"
 "testing"

 "github.com/google/uuid"
 "github.com/stretchr/testify/require"
 "github.com/AlexandrKhromov2005/vault-chat/internal/chat/validator"
)

func TestCanonicalID(t *testing.T) {
 id := uuid.NewString()
 for _, input := range []string{id, strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "urn:uuid:"+id} {
  got, err := validator.CanonicalID(input)
  require.NoError(t, err)
  require.Equal(t, id, got)
 }
 for _, input := range []string{"", "bad", uuid.Nil.String(), " "+id} {
  _, err := validator.CanonicalID(input)
  require.ErrorIs(t, err, validator.ErrInvalidID)
 }
}

func TestChannelName(t *testing.T) {
 for _, name := range []string{"general", "Команда разработки", strings.Repeat("я", 80)} {
  require.NoError(t, validator.ChannelName(name))
 }
 for _, name := range []string{"", " ", " general", "general ", "a\nb", "a\x00b", string([]byte{255}), strings.Repeat("я", 81)} {
  require.ErrorIs(t, validator.ChannelName(name), validator.ErrInvalidName)
 }
}
