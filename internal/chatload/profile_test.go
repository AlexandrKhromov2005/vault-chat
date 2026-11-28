package chatload

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultProfileIsValid(t *testing.T) {
	profile, err := DefaultProfile()
	require.NoError(t, err)
	require.NoError(t, profile.Validate())
	require.Positive(t, profile.Gates.SendP95Ms)
}

func TestProfileValidationRejectsUnreachableOrUnmeasurableLoad(t *testing.T) {
	base, err := DefaultProfile()
	require.NoError(t, err)
	for name, change := range map[string]func(*Profile){
		"odd users":                    func(p *Profile) { p.Users = 121 },
		"groups do not divide users":   func(p *Profile) { p.GroupSize = 7 },
		"tiny groups":                  func(p *Profile) { p.GroupSize = 2 },
		"empty text":                   func(p *Profile) { p.MinTextBytes = 0 },
		"text over the Chat limit":     func(p *Profile) { p.MaxTextBytes = 4097 },
		"inverted text bounds":         func(p *Profile) { p.MinTextBytes, p.MaxTextBytes = 200, 100 },
		"no send rate":                 func(p *Profile) { p.SendRate = 0 },
		"negative reads":               func(p *Profile) { p.ReadRate = -1 },
		"retry share of one":           func(p *Profile) { p.RetryShare = 1 },
		"no series":                    func(p *Profile) { p.Series = 0 },
		"short series":                 func(p *Profile) { p.SeriesSeconds = 5 },
		"per-user budget exceeded":     func(p *Profile) { p.SendRate = 1.5 * float64(p.Users) },
		"ramp exceeds per-user budget": func(p *Profile) { p.Ramp.SendRates = []float64{1.5 * float64(p.Users)} },
		"ramp not increasing":          func(p *Profile) { p.Ramp.SendRates = []float64{p.SendRate * 2, p.SendRate} },
		"reconnect beyond address burst": func(p *Profile) {
			p.Reconnect.Users = AddressBurst
		},
		"reconnect offline longer than the phase": func(p *Profile) {
			p.Reconnect.OfflineSeconds = p.Reconnect.Seconds
		},
		"missing gate": func(p *Profile) { p.Gates.DeliveryP99Ms = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			profile := base
			profile.Ramp.SendRates = append([]float64(nil), base.Ramp.SendRates...)
			change(&profile)
			require.Error(t, profile.Validate())
		})
	}
}

func TestPerUserRequestRateIncludesRetriesAndReads(t *testing.T) {
	profile := Profile{Users: 100, RetryShare: 0.5, ReadRate: 10}
	require.InDelta(t, 0.7, profile.PerUserRate(40), 1e-9)
}
