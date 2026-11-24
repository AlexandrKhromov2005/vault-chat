package chatload

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	requestTimeout = 10 * time.Second
	// settle lets events of the last requests of a phase arrive.
	settle = 2 * time.Second
	// maxInFlight bounds the open-loop generator; beyond it the stand is
	// far past saturation and new operations are counted as overload.
	maxInFlight = 2000
	// reconnectSpread spreads the reconnects of the reconnect phase, as the
	// jittered backoff of real clients does.
	reconnectSpread = 5 * time.Second
)

// Invitation is a registration invitation created by the orchestrator.
type Invitation struct {
	Email string `json:"email"`
	Token string `json:"invitation_token"`
}

// Control is the orchestrator: at the edge of every phase it takes a
// snapshot of the stand. A nil snapshot means none is available.
type Control interface {
	Mark(phase, edge string) (*Snapshot, error)
}

// Config configures a run.
type Config struct {
	Profile     Profile
	GatewayURL  string
	Metrics     map[string]string
	Invitations []Invitation
	Control     Control
	Logger      *slog.Logger
	// TLS is the client TLS configuration; nil uses the system roots.
	TLS *tls.Config
}

// Runner executes a profile against one Gateway.
type Runner struct {
	cfg      Config
	prof     Profile
	wsURL    string
	wsClient *http.Client
	scraper  *scraper
	logger   *slog.Logger
	ledger   *Ledger
	current  atomic.Pointer[PhaseStats]
	users    []*user
	rooms    []*chatRoom
	phaseNo  uint64
}

// NewRunner validates the configuration.
func NewRunner(cfg Config) (*Runner, error) {
	if err := cfg.Profile.Validate(); err != nil {
		return nil, err
	}
	if len(cfg.Invitations) != cfg.Profile.Users {
		return nil, fmt.Errorf("%d invitations for %d users", len(cfg.Invitations), cfg.Profile.Users)
	}
	if cfg.Control == nil || cfg.Logger == nil {
		return nil, errors.New("control and logger are required")
	}
	wsURL := "ws" + strings.TrimPrefix(cfg.GatewayURL, "http") + "/api/v1/ws"
	transport := &http.Transport{TLSClientConfig: cfg.TLS.Clone(), ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	r := &Runner{
		cfg: cfg, prof: cfg.Profile, wsURL: wsURL, wsClient: &http.Client{Transport: transport},
		scraper: newScraper(cfg.Metrics), logger: cfg.Logger, ledger: NewLedger(),
	}
	r.current.Store(NewPhaseStats())
	return r, nil
}

func (r *Runner) stats() *PhaseStats { return r.current.Load() }

// newUserAPI gives every user a client of its own, like separate browsers.
func (r *Runner) newUserAPI() *API {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = r.cfg.TLS.Clone()
	transport.MaxIdleConnsPerHost = 4
	return NewAPI(r.cfg.GatewayURL, &http.Client{Transport: transport, Timeout: requestTimeout})
}

// Run executes setup, warmup, the series, the reconnect phase and the ramp,
// then evaluates the gates.
func (r *Runner) Run(ctx context.Context) (Report, error) {
	report := Report{Profile: r.prof}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer r.closeAll()
	if err := NewAPI(r.cfg.GatewayURL, &http.Client{Transport: &http.Transport{TLSClientConfig: r.cfg.TLS.Clone()}, Timeout: 5 * time.Second}).Ready(withTimeout(ctx, time.Minute)); err != nil {
		return report, err
	}
	setup, err := r.setup(ctx)
	report.Setup = setup
	if err != nil {
		return report, fmt.Errorf("setup: %w", err)
	}
	for _, u := range r.users {
		go r.keepFresh(ctx, u)
	}
	p := r.prof
	if p.WarmupSeconds > 0 {
		if report.Warmup, err = r.phase(ctx, "warmup", p.SendRate, seconds(p.WarmupSeconds), nil); err != nil {
			return report, err
		}
	}
	for i := 1; i <= p.Series; i++ {
		phase, err := r.phase(ctx, fmt.Sprintf("series-%d", i), p.SendRate, seconds(p.SeriesSeconds), nil)
		report.Series = append(report.Series, phase)
		if err != nil {
			return report, err
		}
	}
	reconnect := &ReconnectReport{Users: p.Reconnect.Users}
	if report.Reconnect, err = r.phase(ctx, "reconnect", p.SendRate, seconds(p.Reconnect.Seconds), func(ctx context.Context) {
		r.reconnectSome(ctx, reconnect)
	}); err != nil {
		return report, err
	}
	report.Reconnect.Reconnect = reconnect
	for _, rate := range p.Ramp.SendRates {
		step, err := r.phase(ctx, fmt.Sprintf("ramp-%g", rate), rate, seconds(p.Ramp.StepSeconds), nil)
		report.Ramp = append(report.Ramp, step)
		if err != nil {
			return report, err
		}
	}
	report.Evaluate()
	return report, nil
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }

func withTimeout(ctx context.Context, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(ctx, d)
	context.AfterFunc(ctx, cancel)
	return ctx
}

func (r *Runner) closeAll() {
	for _, u := range r.users {
		r.disconnect(u)
	}
}

// setup creates the accounts, rooms and connections.
func (r *Runner) setup(ctx context.Context) (SetupReport, error) {
	p := r.prof
	report := SetupReport{Users: p.Users, DirectRooms: p.Users / 2, Channels: p.Users / p.GroupSize}
	start := time.Now()
	r.layout(r.cfg.Invitations)
	for _, u := range r.users {
		u.api = r.newUserAPI()
	}
	// Auth hashes two passwords at a time and refuses more with 503.
	err := parallel(ctx, len(r.users), 2, func(ctx context.Context, i int) error {
		u := r.users[i]
		id, err := retryLimited(ctx, func(ctx context.Context) (string, error) {
			return u.api.Register(ctx, u.email, u.name, u.password, r.cfg.Invitations[i].Token)
		})
		if err != nil {
			return fmt.Errorf("register user %d: %w", i, err)
		}
		tokens, err := retryLimited(ctx, func(ctx context.Context) (Tokens, error) { return u.api.Login(ctx, u.email, u.password) })
		if err != nil {
			return fmt.Errorf("login user %d: %w", i, err)
		}
		u.id, u.tokens = id, tokens
		return nil
	})
	report.AccountSeconds = time.Since(start).Seconds()
	if err != nil {
		return report, err
	}
	report.AccessTTLSeconds = math.Round(time.Until(r.users[0].tokens.AccessExpires).Seconds())

	start = time.Now()
	err = parallel(ctx, len(r.rooms), 6, func(ctx context.Context, i int) error {
		room := r.rooms[i]
		owner := room.members[0]
		var err error
		if room.direct {
			room.id, err = retryLimited(ctx, func(ctx context.Context) (string, error) {
				return owner.api.Direct(ctx, owner.access(), room.members[1].id)
			})
			return err
		}
		if room.id, err = retryLimited(ctx, func(ctx context.Context) (string, error) {
			return owner.api.Channel(ctx, owner.access(), fmt.Sprintf("load-%03d", i))
		}); err != nil {
			return err
		}
		for _, member := range room.members[1:] {
			if _, err := retryLimited(ctx, func(ctx context.Context) (struct{}, error) {
				return struct{}{}, owner.api.AddMember(ctx, owner.access(), room.id, member.id)
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return report, fmt.Errorf("create rooms: %w", err)
	}
	report.RoomSeconds = time.Since(start).Seconds()

	start = time.Now()
	var connect Latencies
	report.ConnectRetries, err = r.connectAll(ctx, r.users, &connect)
	report.ConnectSeconds = time.Since(start).Seconds()
	report.Connect = connect.Summary()
	report.Connections = connect.Summary().Count
	if err != nil {
		return report, err
	}
	// The upgrades took tokens of the per-address bucket (2 per second) that
	// are not refunded; let it refill so that the first phase does not start
	// with refused requests.
	select {
	case <-ctx.Done():
		return report, ctx.Err()
	case <-time.After(time.Duration(min(AddressBurst, report.Connections)) * time.Second / 2):
	}
	return report, nil
}

// layout creates the users and rooms of the profile: users 2i and 2i+1 share
// a direct dialog, and consecutive groups of GroupSize users a channel.
func (r *Runner) layout(invitations []Invitation) {
	p := r.prof
	r.users = make([]*user, p.Users)
	for i := range r.users {
		secret := make([]byte, 12)
		_, _ = rand.Read(secret)
		r.users[i] = &user{
			index: i, email: invitations[i].Email, name: fmt.Sprintf("load%04d", i),
			password: "Load1-" + hex.EncodeToString(secret),
		}
	}
	for i := 0; i < p.Users; i += 2 {
		r.rooms = append(r.rooms, &chatRoom{direct: true, members: []*user{r.users[i], r.users[i+1]}})
	}
	for g := 0; g < p.Users/p.GroupSize; g++ {
		r.rooms = append(r.rooms, &chatRoom{members: r.users[g*p.GroupSize : (g+1)*p.GroupSize]})
	}
	for _, room := range r.rooms {
		for _, member := range room.members {
			member.rooms = append(member.rooms, room)
		}
	}
}

// retryLimited repeats a request outside the measured window that a rate
// limit (429) or a full Auth password pool (503, two Argon2id hashes at a
// time) refused, after its Retry-After.
func retryLimited[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	for attempt := 0; ; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		value, err := call(requestCtx)
		cancel()
		var failure *RequestError
		if err == nil || attempt >= 60 || !errors.As(err, &failure) ||
			(failure.Status != http.StatusTooManyRequests && failure.Status != http.StatusServiceUnavailable) {
			return value, err
		}
		select {
		case <-ctx.Done():
			return value, ctx.Err()
		case <-time.After(max(failure.RetryAfter, 250*time.Millisecond)):
		}
	}
}

// parallel runs work(i) for i in [0, n) on at most workers goroutines and
// returns the first error.
func parallel(ctx context.Context, n, workers int, work func(context.Context, int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	next := atomic.Int64{}
	var once sync.Once
	var first error
	var wg sync.WaitGroup
	for range min(workers, n) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n || ctx.Err() != nil {
					return
				}
				if err := work(ctx, i); err != nil {
					once.Do(func() { first = err; cancel() })
					return
				}
			}
		}()
	}
	wg.Wait()
	if first == nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return first
}

// operation is one scheduled request, drawn by the single scheduler so that
// a seed always gives the same sequence.
type operation struct {
	at       time.Duration
	read     bool
	user     *user
	room     *chatRoom
	clientID string
	text     string
	retry    bool
	listRoom bool
}

// schedule draws the operations of a phase. Sends and reads are each a
// Poisson process conditioned on its count: rate*duration operations at
// uniformly random times. The count is exact, so throughput compares with the
// target, and arrivals stay random.
func (r *Runner) schedule(rng *mathrand.Rand, sendRate, readRate float64, duration time.Duration) []operation {
	sends := int(math.Round(sendRate * duration.Seconds()))
	reads := int(math.Round(readRate * duration.Seconds()))
	ops := make([]operation, 0, sends+reads)
	for i := range sends + reads {
		u := r.users[rng.IntN(len(r.users))]
		op := operation{
			at:   time.Duration(rng.Float64() * float64(duration)),
			user: u, room: u.rooms[rng.IntN(len(u.rooms))],
		}
		if i >= sends {
			op.read, op.listRoom = true, rng.IntN(2) == 0
			ops = append(ops, op)
			continue
		}
		var id uuid.UUID
		for j := range id {
			id[j] = byte(rng.Uint32())
		}
		id[6] = id[6]&0x0f | 0x40
		id[8] = id[8]&0x3f | 0x80
		op.clientID, op.text, op.retry = id.String(), r.text(rng), rng.Float64() < r.prof.RetryShare
		ops = append(ops, op)
	}
	slices.SortStableFunc(ops, func(a, b operation) int { return cmp.Compare(a.at, b.at) })
	return ops
}

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789 "

func (r *Runner) text(rng *mathrand.Rand) string {
	n := r.prof.MinTextBytes + rng.IntN(r.prof.MaxTextBytes-r.prof.MinTextBytes+1)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rng.IntN(len(alphabet))]
	}
	b[0] = 'm'
	return string(b)
}

// phase runs one measured phase at sendRate and the profile's read rate.
// during, if set, runs alongside the load and the phase waits for it.
func (r *Runner) phase(ctx context.Context, name string, sendRate float64, duration time.Duration, during func(context.Context)) (PhaseReport, error) {
	report := PhaseReport{Name: name, TargetSendRate: sendRate, TargetReadRate: r.prof.ReadRate}
	// Clients that lost their socket in the previous phase come back first.
	if _, err := r.connectAll(ctx, r.users, nil); err != nil {
		return report, err
	}
	r.phaseNo++
	rng := mathrand.New(mathrand.NewPCG(uint64(r.prof.Seed), r.phaseNo))
	ops := r.schedule(rng, sendRate, r.prof.ReadRate, duration)
	stats := NewPhaseStats()
	r.current.Store(stats)
	r.ledger.SetPhase(stats)
	r.logger.Info("phase", "name", name, "send_rate", sendRate, "operations", len(ops))

	start, err := r.cfg.Control.Mark(name, "start")
	if err != nil {
		return report, err
	}
	before := r.scraper.counters(ctx)
	sampling, stopSampling := context.WithCancel(ctx)
	samples := make(chan Server, 1)
	go func() { samples <- r.scraper.sample(sampling) }()

	begin := time.Now()
	var wg sync.WaitGroup
	if during != nil {
		wg.Add(1)
		go func() { defer wg.Done(); during(ctx) }()
	}
	for _, op := range ops {
		if wait := time.Until(begin.Add(op.at)); wait > 0 {
			select {
			case <-ctx.Done():
				stopSampling()
				return report, ctx.Err()
			case <-time.After(wait):
			}
		}
		if stats.inFlight.Load() >= maxInFlight {
			stats.fail("overload")
			if !op.read {
				stats.sends.Add(1)
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if op.read {
				r.read1(ctx, stats, op)
			} else {
				r.send(ctx, stats, op)
			}
		}()
	}
	if wait := time.Until(begin.Add(duration)); wait > 0 {
		time.Sleep(wait)
	}
	wg.Wait()
	elapsed := time.Since(begin)
	time.Sleep(settle)
	stopSampling()
	server := <-samples
	r.scraper.delta(ctx, before, &server)
	end, err := r.cfg.Control.Mark(name, "end")
	if err != nil {
		return report, err
	}

	report.Seconds = elapsed.Seconds()
	report.Sends = stats.sends.Load()
	report.Acknowledged = stats.acknowledged.Load()
	report.Retries = stats.retries.Load()
	report.Reads = stats.reads.Load()
	report.Throughput = float64(report.Acknowledged) / duration.Seconds()
	report.ThroughputRatio = report.Throughput / sendRate
	report.ReadThroughput = float64(report.Reads) / duration.Seconds()
	report.Send, report.Read, report.Delivery = stats.Send.Summary(), stats.Read.Summary(), stats.Delivery.Summary()
	report.ExpectedDeliveries, report.Deliveries = stats.expected.Load(), stats.deliveries.Load()
	if report.ExpectedDeliveries > 0 {
		report.DeliveryRatio = float64(report.Deliveries) / float64(report.ExpectedDeliveries)
	}
	report.DeliveriesPerSec = float64(report.Deliveries) / duration.Seconds()
	report.LiveDuplicates = stats.liveDuplicates.Load()
	stats.mu.Lock()
	report.Errors, report.UnexpectedCloses = stats.errors, stats.closures
	stats.errors, stats.closures = map[string]int64{}, map[string]int64{}
	stats.mu.Unlock()
	report.Requests = report.Sends + report.Retries + report.Reads
	var failed int64
	for _, n := range report.Errors {
		failed += n
	}
	if report.Requests > 0 {
		report.ErrorRate = float64(failed) / float64(report.Requests)
	}
	report.MaxInFlight = stats.maxInFlight.Load()
	report.Server = server
	if start != nil && end != nil {
		resources := ComputeResources(*start, *end)
		report.Resources = &resources
	}

	// Outside the measured window: every client catches up as the web
	// client does after a gap, then all rooms are checked.
	r.current.Store(NewPhaseStats())
	report.CatchUp, err = r.catchUpAll(ctx)
	if err != nil {
		return report, fmt.Errorf("%s catch-up: %w", name, err)
	}
	if report.Integrity, err = r.verify(ctx); err != nil {
		return report, fmt.Errorf("%s verification: %w", name, err)
	}
	r.logger.Info("phase done", "name", name, "throughput", math.Round(report.Throughput*10)/10,
		"send_p95_ms", report.Send.P95, "delivery_p95_ms", report.Delivery.P95, "errors", failed,
		"integrity_violations", report.Integrity.Violations())
	return report, nil
}

func (r *Runner) send(ctx context.Context, stats *PhaseStats, op operation) {
	stats.begin()
	defer stats.end()
	stats.sends.Add(1)
	var connected int64
	for _, member := range op.room.members {
		if member.connected() {
			connected++
		}
	}
	stats.expected.Add(connected)
	token := op.user.access()
	attempts := 1
	if op.retry {
		attempts = 2
	}
	for attempt := range attempts {
		start := time.Now()
		if attempt == 0 {
			r.ledger.Sending(op.clientID, op.room.id, stats, start)
		} else {
			stats.retries.Add(1)
		}
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		m, err := op.user.api.Send(requestCtx, token, op.room.id, op.clientID, op.text)
		cancel()
		stats.Send.Add(time.Since(start))
		if err != nil {
			stats.fail(errorKind(err))
			return
		}
		if attempt == 0 {
			stats.acknowledged.Add(1)
		}
		r.ledger.Acknowledged(op.clientID, m)
	}
}

func (r *Runner) read1(ctx context.Context, stats *PhaseStats, op operation) {
	stats.begin()
	defer stats.end()
	stats.reads.Add(1)
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	start := time.Now()
	var err error
	if op.listRoom {
		_, err = op.user.api.Rooms(requestCtx, op.user.access(), "", 50)
	} else {
		_, _, err = op.user.api.History(requestCtx, op.user.access(), op.room.id, -1, 50)
	}
	stats.Read.Add(time.Since(start))
	if err != nil {
		stats.fail(errorKind(err))
	}
}

// catchUp follows docs/chat/realtime.md: list the rooms page by page, then
// read every room after the sequence the client has.
func (r *Runner) catchUp(ctx context.Context, u *user) (requests, recovered int64, err error) {
	token := u.access()
	var rooms []string
	cursor := ""
	for {
		page, err := retryLimited(ctx, func(ctx context.Context) (RoomPage, error) { return u.api.Rooms(ctx, token, cursor, 100) })
		requests++
		if err != nil {
			return requests, recovered, err
		}
		for _, room := range page.Rooms {
			rooms = append(rooms, room.ID)
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	for _, room := range rooms {
		after := r.ledger.Known(u.id, room)
		for {
			var messages []Message
			var next string
			messages, next, err = retryLimitedPage(ctx, u, token, room, after)
			requests++
			if err != nil {
				return requests, recovered, err
			}
			for _, m := range messages {
				if r.ledger.CatchUp(u.id, m) {
					recovered++
				}
			}
			if next == "" {
				break
			}
			if after, err = strconv.ParseInt(next, 10, 64); err != nil {
				return requests, recovered, fmt.Errorf("next_cursor %q: %w", next, err)
			}
		}
	}
	return requests, recovered, nil
}

func retryLimitedPage(ctx context.Context, u *user, token, room string, after int64) ([]Message, string, error) {
	type page struct {
		messages []Message
		next     string
	}
	p, err := retryLimited(ctx, func(ctx context.Context) (page, error) {
		messages, next, err := u.api.History(ctx, token, room, after, 100)
		return page{messages, next}, err
	})
	return p.messages, p.next, err
}

func (r *Runner) catchUpAll(ctx context.Context) (CatchUp, error) {
	start := time.Now()
	var requests, recovered atomic.Int64
	err := parallel(ctx, len(r.users), 16, func(ctx context.Context, i int) error {
		n, m, err := r.catchUp(ctx, r.users[i])
		requests.Add(n)
		recovered.Add(m)
		return err
	})
	return CatchUp{Requests: requests.Load(), Recovered: recovered.Load(), Seconds: time.Since(start).Seconds()}, err
}

// verify reads the complete history of every room and checks it against
// what was sent and what every member has.
func (r *Runner) verify(ctx context.Context) (Integrity, error) {
	results := make([]Integrity, len(r.rooms))
	err := parallel(ctx, len(r.rooms), 8, func(ctx context.Context, i int) error {
		room := r.rooms[i]
		reader := room.members[0]
		var history []Message
		var after int64
		for {
			messages, next, err := retryLimitedPage(ctx, reader, reader.access(), room.id, after)
			if err != nil {
				return err
			}
			history = append(history, messages...)
			if next == "" {
				break
			}
			if after, err = strconv.ParseInt(next, 10, 64); err != nil {
				return err
			}
		}
		results[i] = r.ledger.Verify(room.id, room.memberIDs(), history)
		return nil
	})
	var total Integrity
	for _, result := range results {
		total = total.Add(result)
	}
	return total, err
}

// reconnectSome disconnects a deterministic subset of clients while the load
// continues, keeps them offline, then reconnects them over reconnectSpread
// and catches each up.
func (r *Runner) reconnectSome(ctx context.Context, report *ReconnectReport) {
	p := r.prof.Reconnect
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	step := len(r.users) / p.Users
	var offline []*user
	for i := 0; i < p.Users; i++ {
		offline = append(offline, r.users[i*step+1])
	}
	for _, u := range offline {
		r.disconnect(u)
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(seconds(p.OfflineSeconds)):
	}
	var reconnect Latencies
	var reconnected, recovered atomic.Int64
	var retries atomic.Int64
	var wg sync.WaitGroup
	for i, u := range offline {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectSpread * time.Duration(i) / time.Duration(len(offline))):
			}
			start := time.Now()
			if err := r.connectRetrying(ctx, u, nil, &retries); err != nil {
				r.stats().fail("reconnect")
				r.logger.Warn("reconnect failed", "user", u.index, "error", err)
				return
			}
			_, n, err := r.catchUp(ctx, u)
			if err != nil {
				r.stats().fail("catch_up_" + errorKind(err))
				return
			}
			reconnect.Add(time.Since(start))
			reconnected.Add(1)
			recovered.Add(n)
		}()
	}
	wg.Wait()
	report.Reconnected = int(reconnected.Load())
	report.Reconnect = reconnect.Summary()
	report.Recovered = recovered.Load()
}

// MarshalLine encodes v as one line of the orchestrator protocol.
func MarshalLine(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	return append(data, '\n'), err
}
