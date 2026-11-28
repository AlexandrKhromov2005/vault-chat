package chatload

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStdioControlExchangesMarksForSnapshots(t *testing.T) {
	in := strings.NewReader(`{"invitations":[{"email":"load0000@example.com","invitation_token":"t"}],"stand":{"cpus":2}}
{"snapshot":{"time":1.5,"containers":{"chat":{"cpu_ns":7}}}}
{"error":"docker unavailable"}
`)
	var out bytes.Buffer
	control := NewStdioControl(in, &out)

	init, err := control.ReadInit()
	require.NoError(t, err)
	require.Equal(t, []Invitation{{Email: "load0000@example.com", Token: "t"}}, init.Invitations)
	require.JSONEq(t, `{"cpus":2}`, string(init.Stand))

	snapshot, err := control.Mark("series-1", "start")
	require.NoError(t, err)
	require.InDelta(t, 1.5, snapshot.Time, 1e-9)
	require.Equal(t, int64(7), snapshot.Containers["chat"].CPUNanoseconds)

	_, err = control.Mark("series-1", "end")
	require.ErrorContains(t, err, "docker unavailable")
	_, err = control.Mark("series-2", "start")
	require.Error(t, err, "the orchestrator went away")

	require.Equal(t, `{"edge":"start","event":"mark","phase":"series-1"}
{"edge":"end","event":"mark","phase":"series-1"}
{"edge":"start","event":"mark","phase":"series-2"}
`, out.String())
}
