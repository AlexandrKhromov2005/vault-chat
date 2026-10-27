package mail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type queueStub struct {
	mu                              sync.Mutex
	jobs                            []*Job
	completed, retried              []string
	next                            []time.Time
	claimErr, completeErr, retryErr error
	claims                          int
}

func (q *queueStub) Claim(context.Context) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.claims++
	if len(q.jobs) == 0 {
		return nil, q.claimErr
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job, q.claimErr
}
func (q *queueStub) Complete(_ context.Context, id, lease string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed = append(q.completed, id+":"+lease)
	return q.completeErr
}
func (q *queueStub) Retry(_ context.Context, id, lease string, next time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.retried = append(q.retried, id+":"+lease)
	q.next = append(q.next, next)
	return q.retryErr
}

type senderFunc func(context.Context, Message) error

func (s senderFunc) Send(ctx context.Context, message Message) error { return s(ctx, message) }

func testJob(t *testing.T, attempts int) *Job {
	t.Helper()
	payload, err := testBox(t).Seal(Message{To: "fixture@example.invalid", Subject: "Verify", Text: "synthetic-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return &Job{ID: "job-fixture", Lease: "lease-fixture", Payload: payload, Attempts: attempts, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestWorkerSendsAndCompletesWithLeaseAndDeadline(t *testing.T) {
	q := &queueStub{jobs: []*Job{testJob(t, 1)}}
	sent := 0
	worker := NewWorker(q, senderFunc(func(ctx context.Context, m Message) error {
		sent++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || m.Text != "synthetic-secret" {
			t.Error("missing send bounds or wrong message")
		}
		return nil
	}), testBox(t), nil)
	worked, err := worker.Step(context.Background())
	if err != nil || !worked || sent != 1 || len(q.completed) != 1 || q.completed[0] != "job-fixture:lease-fixture" {
		t.Fatalf("delivery/lease completion failed: worked=%v sent=%d complete=%v err=%v", worked, sent, q.completed, err)
	}
}

func TestWorkerDropsExpiredCorruptAndExhaustedJobsWithoutSending(t *testing.T) {
	for _, change := range []func(*Job){
		func(j *Job) { j.ExpiresAt = time.Now().Add(-time.Second) },
		func(j *Job) { j.Payload = []byte("corrupt") },
		func(j *Job) { j.Attempts = 6 },
	} {
		job := testJob(t, 1)
		change(job)
		q := &queueStub{jobs: []*Job{job}}
		worker := NewWorker(q, senderFunc(func(context.Context, Message) error { t.Error("sent invalid job"); return nil }), testBox(t), nil)
		worked, err := worker.Step(context.Background())
		if err != nil || !worked || len(q.next) != 1 || !q.next[0].IsZero() || len(q.completed) != 0 {
			t.Fatal("invalid job was not permanently dropped")
		}
	}
}

func TestWorkerRetriesBoundedlyAndLogsNoDependencySecrets(t *testing.T) {
	for attempts := 1; attempts <= 5; attempts++ {
		q := &queueStub{jobs: []*Job{testJob(t, attempts)}}
		var output bytes.Buffer
		secret := "550 fixture@example.invalid synthetic-secret"
		worker := NewWorker(q, senderFunc(func(context.Context, Message) error { return errors.New(secret) }), testBox(t), slog.New(slog.NewTextHandler(&output, nil)))
		before := time.Now()
		worked, err := worker.Step(context.Background())
		if err != nil || !worked || len(q.next) != 1 {
			t.Fatalf("failed to retry: %v", err)
		}
		if attempts == 5 {
			if !q.next[0].IsZero() {
				t.Fatal("retried beyond fifth send")
			}
		} else {
			delay := time.Minute * time.Duration(1<<uint(attempts-1))
			if q.next[0].Before(before.Add(delay)) || q.next[0].After(time.Now().Add(delay)) {
				t.Fatalf("wrong exponential retry delay for attempt %d", attempts)
			}
		}
		if strings.Contains(output.String(), "synthetic-secret") || strings.Contains(output.String(), "fixture@example.invalid") || strings.Contains(output.String(), "550") {
			t.Fatal("logs contain SMTP reply or message data")
		}
	}
}

func TestWorkerQueueFailuresStayOpaqueAndIdentifyClaimedJob(t *testing.T) {
	secret := errors.New("database unavailable: synthetic-secret fixture@example.invalid")
	for _, stage := range []string{"claim", "complete", "retry"} {
		t.Run(stage, func(t *testing.T) {
			q := &queueStub{jobs: []*Job{testJob(t, 1)}}
			switch stage {
			case "claim":
				q.claimErr = secret
			case "complete":
				q.completeErr = secret
			case "retry":
				q.retryErr = secret
			}
			var output bytes.Buffer
			worker := NewWorker(q, senderFunc(func(context.Context, Message) error {
				if stage == "retry" {
					return secret
				}
				return nil
			}), testBox(t), slog.New(slog.NewTextHandler(&output, nil)))
			_, err := worker.Step(context.Background())
			if err == nil || strings.Contains(err.Error(), "synthetic-secret") ||
				strings.Contains(output.String(), "synthetic-secret") || strings.Contains(output.String(), "fixture@example.invalid") {
				t.Fatal("queue error was lost or disclosed")
			}
			if stage != "claim" && (!strings.Contains(output.String(), "job-fixture") || !strings.Contains(output.String(), "attempts=1")) {
				t.Fatal("claimed-job failure lacks safe ID/attempt diagnostics")
			}
		})
	}
}

func TestWorkerHonorsJobExpiryAndCanceledContext(t *testing.T) {
	job := testJob(t, 1)
	job.ExpiresAt = time.Now().Add(30 * time.Millisecond)
	q := &queueStub{jobs: []*Job{job}}
	worker := NewWorker(q, senderFunc(func(ctx context.Context, _ Message) error {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(job.ExpiresAt) {
			t.Error("send outlives job expiry")
		}
		<-ctx.Done()
		return ctx.Err()
	}), testBox(t), nil)
	if worked, err := worker.Step(context.Background()); err != nil || !worked || len(q.next) != 1 || !q.next[0].IsZero() {
		t.Fatal("expired send was not permanently dropped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	claims := q.claims
	if _, err := worker.Step(ctx); !errors.Is(err, context.Canceled) || q.claims != claims {
		t.Fatal("canceled step claimed work")
	}
}

func TestWorkerSerializesConcurrentStepsAndStopsIdleRun(t *testing.T) {
	q := &queueStub{jobs: []*Job{testJob(t, 1), testJob(t, 1)}}
	var active atomic.Int32
	worker := NewWorker(q, senderFunc(func(ctx context.Context, _ Message) error {
		if active.Add(1) != 1 {
			t.Error("parallel mail sends")
		}
		defer active.Add(-1)
		select {
		case <-time.After(10 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}), testBox(t), nil)
	var done sync.WaitGroup
	for range 2 {
		done.Add(1)
		go func() {
			defer done.Done()
			if _, err := worker.Step(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	done.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	worker.Run(ctx)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.claims > 3 {
		t.Fatalf("empty queue hot loop: %d claims", q.claims)
	}
}
