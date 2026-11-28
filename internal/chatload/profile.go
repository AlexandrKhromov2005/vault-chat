package chatload

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// The production Gateway limits the profile must stay within, since all
// synthetic clients share one address (docs/gateway/rate-limits.md).
const (
	// PerUserBudget is GATEWAY_RATE_LIMIT_PER_MINUTE (120) per second; the
	// per-user bucket is never refunded.
	PerUserBudget = 2.0
	// AddressBurst is GATEWAY_RATE_LIMIT_BURST: WebSocket upgrades take a
	// per-address token that is not refunded, so at most this many clients
	// of one address reconnect at once.
	AddressBurst = 30
	// maxTextRunes is the Chat limit for a message text.
	maxTextRunes = 4096
)

//go:embed profile.json
var defaultProfile []byte

// Profile is the load profile and its acceptance gates.
type Profile struct {
	// Seed makes the sequence of operations, texts and client ids repeatable.
	Seed int64 `json:"seed"`
	// Users is the number of synthetic users, each with one WebSocket.
	// Users are paired into direct dialogs and split into channels of
	// GroupSize members, so each user is in one of each.
	Users        int `json:"users"`
	GroupSize    int `json:"group_size"`
	MinTextBytes int `json:"min_text_bytes"`
	MaxTextBytes int `json:"max_text_bytes"`
	// SendRate is the target of new messages per second over all users;
	// RetryShare of them are sent again with the same client message id.
	SendRate   float64 `json:"send_rate"`
	RetryShare float64 `json:"retry_share"`
	// ReadRate is reads per second: half room lists, half history pages.
	ReadRate      float64          `json:"read_rate"`
	WarmupSeconds int              `json:"warmup_seconds"`
	Series        int              `json:"series"`
	SeriesSeconds int              `json:"series_seconds"`
	Reconnect     ReconnectProfile `json:"reconnect"`
	Ramp          RampProfile      `json:"ramp"`
	Gates         Gates            `json:"gates"`
}

// ReconnectProfile disconnects Users clients for OfflineSeconds during a
// phase of Seconds at the target rate; they then reconnect and catch up.
type ReconnectProfile struct {
	Users          int `json:"users"`
	OfflineSeconds int `json:"offline_seconds"`
	Seconds        int `json:"seconds"`
}

// RampProfile raises the send rate step by step past the target to find
// where the stand saturates. Its steps are reported, not gated.
type RampProfile struct {
	SendRates   []float64 `json:"send_rates"`
	StepSeconds int       `json:"step_seconds"`
}

// Gates are the acceptance thresholds of every series and of the reconnect
// phase. Latencies are in milliseconds, ratios between 0 and 1.
type Gates struct {
	SendP95Ms     float64 `json:"send_p95_ms"`
	SendP99Ms     float64 `json:"send_p99_ms"`
	ReadP95Ms     float64 `json:"read_p95_ms"`
	ReadP99Ms     float64 `json:"read_p99_ms"`
	DeliveryP95Ms float64 `json:"delivery_p95_ms"`
	DeliveryP99Ms float64 `json:"delivery_p99_ms"`
	// MaxErrorRate is failed requests over all requests.
	MaxErrorRate float64 `json:"max_error_rate"`
	// MinThroughputRatio is acknowledged new messages over the target.
	MinThroughputRatio float64 `json:"min_throughput_ratio"`
	// MinDeliveryRatio is live events received over the events expected by
	// connected members; catch-up must recover the rest in any case.
	MinDeliveryRatio float64 `json:"min_delivery_ratio"`
	// MaxThroughputCV and MaxP95Spread bound how much series differ.
	MaxThroughputCV float64 `json:"max_throughput_cv"`
	MaxP95Spread    float64 `json:"max_p95_spread"`
	// MaxMemoryRatio bounds the peak memory of a container over its limit.
	MaxMemoryRatio float64 `json:"max_memory_ratio"`
	// MaxReconnectMs bounds reconnecting and catching up one client.
	MaxReconnectMs float64 `json:"max_reconnect_ms"`
}

// DefaultProfile returns the committed profile (profile.json).
func DefaultProfile() (Profile, error) {
	return ParseProfile(defaultProfile)
}

// ParseProfile decodes and validates a profile; unknown fields are errors.
func ParseProfile(data []byte) (Profile, error) {
	var profile Profile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	return profile, profile.Validate()
}

// PerUserRate is the average number of requests per second and user at a
// send rate.
func (p Profile) PerUserRate(sendRate float64) float64 {
	return (sendRate*(1+p.RetryShare) + p.ReadRate) / float64(p.Users)
}

// Validate rejects a profile that cannot be generated or measured, or that
// would run into the production rate limits instead of the stand's capacity.
func (p Profile) Validate() error {
	var problems []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			problems = append(problems, fmt.Errorf(format, args...))
		}
	}
	check(p.Users >= 4 && p.Users%2 == 0, "users must be even and at least 4")
	check(p.GroupSize >= 3 && p.Users%max(p.GroupSize, 1) == 0, "group_size must be at least 3 and divide users")
	check(p.MinTextBytes >= 1 && p.MaxTextBytes <= maxTextRunes && p.MinTextBytes <= p.MaxTextBytes,
		"text bytes must satisfy 1 <= min <= max <= %d", maxTextRunes)
	check(p.SendRate > 0, "send_rate must be positive")
	check(p.ReadRate >= 0, "read_rate must not be negative")
	check(p.RetryShare >= 0 && p.RetryShare < 1, "retry_share must be in [0, 1)")
	check(p.WarmupSeconds >= 0, "warmup_seconds must not be negative")
	check(p.Series >= 1, "at least one series is required")
	check(p.SeriesSeconds >= 10, "series_seconds must be at least 10")
	// A quarter of the budget is left for random bursts of one user.
	budget := PerUserBudget * 0.75
	if p.Users > 0 {
		check(p.PerUserRate(p.SendRate) <= budget, "send_rate needs %.2f requests/s per user, over %.2f", p.PerUserRate(p.SendRate), budget)
		for _, rate := range p.Ramp.SendRates {
			check(p.PerUserRate(rate) <= budget, "ramp rate %.0f needs %.2f requests/s per user, over %.2f", rate, p.PerUserRate(rate), budget)
		}
	}
	check(slices.IsSorted(p.Ramp.SendRates) && (len(p.Ramp.SendRates) == 0 || p.Ramp.SendRates[0] > p.SendRate) &&
		len(slices.Compact(slices.Clone(p.Ramp.SendRates))) == len(p.Ramp.SendRates),
		"ramp send_rates must increase from above send_rate")
	check(len(p.Ramp.SendRates) == 0 || p.Ramp.StepSeconds >= 10, "ramp step_seconds must be at least 10")
	check(p.Reconnect.Users >= 1 && p.Reconnect.Users < AddressBurst && p.Reconnect.Users <= p.Users,
		"reconnect users must be between 1 and %d", AddressBurst-1)
	// 5 s before the disconnect and 5 s to spread the reconnects.
	check(p.Reconnect.OfflineSeconds >= 1 && p.Reconnect.Seconds >= p.Reconnect.OfflineSeconds+10,
		"reconnect seconds must exceed offline_seconds by at least 10")
	g := p.Gates
	for name, value := range map[string]float64{
		"send_p95_ms": g.SendP95Ms, "send_p99_ms": g.SendP99Ms, "read_p95_ms": g.ReadP95Ms, "read_p99_ms": g.ReadP99Ms,
		"delivery_p95_ms": g.DeliveryP95Ms, "delivery_p99_ms": g.DeliveryP99Ms, "max_error_rate": g.MaxErrorRate,
		"min_throughput_ratio": g.MinThroughputRatio, "min_delivery_ratio": g.MinDeliveryRatio,
		"max_throughput_cv": g.MaxThroughputCV, "max_p95_spread": g.MaxP95Spread, "max_memory_ratio": g.MaxMemoryRatio,
		"max_reconnect_ms": g.MaxReconnectMs,
	} {
		check(value > 0, "gate %s must be positive", name)
	}
	return errors.Join(problems...)
}
