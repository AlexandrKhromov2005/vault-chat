package chatload

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Report is the machine-readable result of a run.
type Report struct {
	Profile Profile `json:"profile"`
	// Stand describes the machine, versions and limits; the orchestrator
	// fills it in.
	Stand     json.RawMessage `json:"stand,omitempty"`
	Setup     SetupReport     `json:"setup"`
	Warmup    PhaseReport     `json:"warmup"`
	Series    []PhaseReport   `json:"series"`
	Reconnect PhaseReport     `json:"reconnect"`
	Ramp      []PhaseReport   `json:"ramp,omitempty"`

	Repeatability Repeatability `json:"repeatability"`
	Capacity      Capacity      `json:"capacity"`
	Violations    []Violation   `json:"violations"`
	Passed        bool          `json:"passed"`
}

// SetupReport describes the synthetic data and how long it took to create.
type SetupReport struct {
	Users            int     `json:"users"`
	DirectRooms      int     `json:"direct_rooms"`
	Channels         int     `json:"channels"`
	Connections      int     `json:"connections"`
	AccountSeconds   float64 `json:"account_seconds"`
	RoomSeconds      float64 `json:"room_seconds"`
	ConnectSeconds   float64 `json:"connect_seconds"`
	ConnectRetries   int64   `json:"connect_retries"`
	Connect          Summary `json:"connect"`
	AccessTTLSeconds float64 `json:"access_ttl_seconds"`
}

// PhaseReport is the measurement of one phase.
type PhaseReport struct {
	Name           string  `json:"name"`
	Seconds        float64 `json:"seconds"`
	TargetSendRate float64 `json:"target_send_rate"`
	TargetReadRate float64 `json:"target_read_rate"`
	// Sends counts new messages; Retries the repeated sends of them.
	Sends        int64 `json:"sends"`
	Acknowledged int64 `json:"acknowledged"`
	Retries      int64 `json:"retries"`
	Reads        int64 `json:"reads"`
	Requests     int64 `json:"requests"`
	// Throughput is acknowledged new messages per second.
	Throughput      float64 `json:"throughput"`
	ThroughputRatio float64 `json:"throughput_ratio"`
	ReadThroughput  float64 `json:"read_throughput"`
	Send            Summary `json:"send"`
	Read            Summary `json:"read"`
	Delivery        Summary `json:"delivery"`
	// Deliveries counts live events for messages of the phase; Expected the
	// members connected when each message was sent.
	ExpectedDeliveries int64   `json:"expected_deliveries"`
	Deliveries         int64   `json:"deliveries"`
	DeliveryRatio      float64 `json:"delivery_ratio"`
	DeliveriesPerSec   float64 `json:"deliveries_per_second"`
	LiveDuplicates     int64   `json:"live_duplicates"`
	// Errors counts failed requests by kind; ErrorRate is their share.
	Errors    map[string]int64 `json:"errors"`
	ErrorRate float64          `json:"error_rate"`
	// UnexpectedCloses counts WebSocket closes the generator did not ask
	// for, by close code.
	UnexpectedCloses map[string]int64 `json:"unexpected_closes"`
	// MaxInFlight is the largest number of concurrent requests: the client
	// side queue of the open-loop generator.
	MaxInFlight int64      `json:"max_in_flight"`
	Integrity   Integrity  `json:"integrity"`
	CatchUp     CatchUp    `json:"catch_up"`
	Server      Server     `json:"server"`
	Resources   *Resources `json:"resources,omitempty"`
	// Reconnect is set for the reconnect phase only.
	Reconnect *ReconnectReport `json:"reconnect,omitempty"`
}

// CatchUp describes the catch-up of every client after the phase.
type CatchUp struct {
	Requests  int64   `json:"requests"`
	Recovered int64   `json:"recovered"`
	Seconds   float64 `json:"seconds"`
}

// Server holds what the services report about themselves during a phase.
type Server struct {
	EventQueueMax      float64 `json:"event_queue_max"`
	EventQueueCapacity float64 `json:"event_queue_capacity"`
	EventsPublished    float64 `json:"events_published"`
	EventsDropped      float64 `json:"events_dropped"`
	PublishFailures    float64 `json:"events_publish_failures"`
	WebSocketsMin      float64 `json:"websockets_min"`
	WebSocketsMax      float64 `json:"websockets_max"`
	Gateway5xx         float64 `json:"gateway_5xx"`
	Gateway429         float64 `json:"gateway_429"`
	AuthRPCs           float64 `json:"auth_rpcs"`
	ChatRPCs           float64 `json:"chat_rpcs"`
	Scrapes            int     `json:"scrapes"`
	ScrapeErrors       int     `json:"scrape_errors"`
}

// ReconnectReport describes the clients disconnected during the phase.
type ReconnectReport struct {
	Users       int `json:"users"`
	Reconnected int `json:"reconnected"`
	// Reconnect is the time from dialing to the end of catch-up.
	Reconnect Summary `json:"reconnect"`
	Recovered int64   `json:"recovered"`
}

// Repeatability compares the series.
type Repeatability struct {
	ThroughputCV      float64 `json:"throughput_cv"`
	SendP95Spread     float64 `json:"send_p95_spread"`
	ReadP95Spread     float64 `json:"read_p95_spread"`
	DeliveryP95Spread float64 `json:"delivery_p95_spread"`
}

// Capacity summarizes the series and the ramp: the highest send rate that
// met the latency, error and throughput gates, the first that did not, and
// which container was busiest at the highest rate measured.
type Capacity struct {
	HighestPassingRate float64  `json:"highest_passing_rate"`
	FirstFailingRate   float64  `json:"first_failing_rate,omitempty"`
	FailingGates       []string `json:"failing_gates,omitempty"`
	MeasuredRate       float64  `json:"measured_rate"`
	Busiest            string   `json:"busiest"`
	BusiestCPUCores    float64  `json:"busiest_cpu_cores"`
	StandCPUCores      float64  `json:"stand_cpu_cores"`
}

// Violation is one gate a phase did not meet.
type Violation struct {
	Phase string  `json:"phase"`
	Gate  string  `json:"gate"`
	Value float64 `json:"value"`
	Limit float64 `json:"limit"`
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: %s = %g (limit %g)", v.Phase, v.Gate, v.Value, v.Limit)
}

// Evaluate checks every series and the reconnect phase against the gates,
// compares the series and summarizes the capacity.
func (r *Report) Evaluate() {
	g := r.Profile.Gates
	var violations []Violation
	for _, phase := range r.Series {
		violations = append(violations, phaseViolations(phase, g, false)...)
	}
	violations = append(violations, phaseViolations(r.Reconnect, g, true)...)

	var throughput, send, read, delivery []float64
	for _, phase := range r.Series {
		throughput = append(throughput, phase.Throughput)
		send = append(send, phase.Send.P95)
		read = append(read, phase.Read.P95)
		delivery = append(delivery, phase.Delivery.P95)
	}
	r.Repeatability = Repeatability{
		ThroughputCV: coefficientOfVariation(throughput), SendP95Spread: spread(send),
		ReadP95Spread: spread(read), DeliveryP95Spread: spread(delivery),
	}
	add := func(gate string, value, limit float64) {
		if value > limit {
			violations = append(violations, Violation{Phase: "series", Gate: gate, Value: value, Limit: limit})
		}
	}
	add("max_throughput_cv", r.Repeatability.ThroughputCV, g.MaxThroughputCV)
	add("max_p95_spread:send", r.Repeatability.SendP95Spread, g.MaxP95Spread)
	add("max_p95_spread:read", r.Repeatability.ReadP95Spread, g.MaxP95Spread)
	add("max_p95_spread:delivery", r.Repeatability.DeliveryP95Spread, g.MaxP95Spread)

	r.Capacity = capacity(append(append([]PhaseReport(nil), r.Series...), r.Ramp...), g)
	r.Violations = violations
	r.Passed = len(violations) == 0
}

// phaseViolations checks one phase. Clients offline during the reconnect
// phase cannot receive live events, so its delivery ratio is not gated;
// catch-up integrity is.
func phaseViolations(p PhaseReport, g Gates, reconnect bool) []Violation {
	var violations []Violation
	over := func(gate string, value, limit float64) {
		if value > limit {
			violations = append(violations, Violation{Phase: p.Name, Gate: gate, Value: value, Limit: limit})
		}
	}
	under := func(gate string, value, limit float64) {
		if value < limit {
			violations = append(violations, Violation{Phase: p.Name, Gate: gate, Value: value, Limit: limit})
		}
	}
	under("send_count", float64(p.Send.Count), 1)
	if p.Send.Count > 0 {
		over("send_p95_ms", p.Send.P95, g.SendP95Ms)
		over("send_p99_ms", p.Send.P99, g.SendP99Ms)
	}
	if p.Read.Count > 0 {
		over("read_p95_ms", p.Read.P95, g.ReadP95Ms)
		over("read_p99_ms", p.Read.P99, g.ReadP99Ms)
	}
	if p.Delivery.Count > 0 {
		over("delivery_p95_ms", p.Delivery.P95, g.DeliveryP95Ms)
		over("delivery_p99_ms", p.Delivery.P99, g.DeliveryP99Ms)
	}
	over("max_error_rate", p.ErrorRate, g.MaxErrorRate)
	under("min_throughput_ratio", p.ThroughputRatio, g.MinThroughputRatio)
	if !reconnect {
		under("min_delivery_ratio", p.DeliveryRatio, g.MinDeliveryRatio)
	}
	over("live_duplicates", float64(p.LiveDuplicates), 0)
	over("integrity", float64(p.Integrity.Violations()), 0)
	over("events_dropped", p.Server.EventsDropped, 0)
	over("events_publish_failures", p.Server.PublishFailures, 0)
	var closes int64
	for _, n := range p.UnexpectedCloses {
		closes += n
	}
	over("unexpected_closes", float64(closes), 0)
	if p.Server.Scrapes == 0 || p.Server.ScrapeErrors > 0 {
		violations = append(violations, Violation{Phase: p.Name, Gate: "metrics", Value: float64(p.Server.ScrapeErrors), Limit: 0})
	}
	if p.Resources == nil {
		violations = append(violations, Violation{Phase: p.Name, Gate: "resources", Value: 0, Limit: 1})
	} else {
		names := make([]string, 0, len(p.Resources.Containers))
		for name := range p.Resources.Containers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			usage := p.Resources.Containers[name]
			over("max_memory_ratio:"+name, usage.MemoryRatio, g.MaxMemoryRatio)
			over("restarts:"+name, float64(usage.Restarts), 0)
			if usage.OOMKilled {
				violations = append(violations, Violation{Phase: p.Name, Gate: "oom_killed:" + name, Value: 1, Limit: 0})
			}
		}
	}
	if p.Reconnect != nil {
		under("reconnected", float64(p.Reconnect.Reconnected), float64(p.Reconnect.Users))
		over("max_reconnect_ms", p.Reconnect.Reconnect.Max, g.MaxReconnectMs)
	}
	return violations
}

func capacity(phases []PhaseReport, g Gates) Capacity {
	var result Capacity
	sort.SliceStable(phases, func(i, j int) bool { return phases[i].TargetSendRate < phases[j].TargetSendRate })
	for _, p := range phases {
		violations := phaseViolations(p, g, false)
		if len(violations) == 0 {
			if result.FirstFailingRate == 0 {
				result.HighestPassingRate = max(result.HighestPassingRate, p.TargetSendRate)
			}
		} else if result.FirstFailingRate == 0 {
			result.FirstFailingRate = p.TargetSendRate
			for _, v := range violations {
				result.FailingGates = append(result.FailingGates, v.Gate)
			}
		}
		if p.TargetSendRate >= result.MeasuredRate && p.Resources != nil {
			result.MeasuredRate = p.TargetSendRate
			result.Busiest = p.Resources.Busiest
			result.BusiestCPUCores = p.Resources.Containers[p.Resources.Busiest].CPUCores
			result.StandCPUCores = p.Resources.StandCPUCores
		}
	}
	return result
}
