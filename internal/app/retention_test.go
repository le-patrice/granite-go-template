package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeRetentionStore struct {
	cutoffs map[string]time.Time
	errs    map[string]error
}

func (f *fakeRetentionStore) record(name string, cutoff time.Time) (int64, error) {
	f.cutoffs[name] = cutoff
	return 1, f.errs[name]
}

func (f *fakeRetentionStore) DeleteExpiredIdempotencyKeys(_ context.Context, c time.Time) (int64, error) {
	return f.record("idempotency_keys", c)
}

func (f *fakeRetentionStore) DeleteExpiredRefreshTokens(_ context.Context, c time.Time) (int64, error) {
	return f.record("refresh_tokens", c)
}

func (f *fakeRetentionStore) DeleteOldInboundEvents(_ context.Context, c time.Time) (int64, error) {
	return f.record("inbound_events", c)
}

func newTestRetentionWorker(store *fakeRetentionStore, now time.Time) *retentionWorker {
	return &retentionWorker{
		store: store,
		policy: retentionPolicy{
			IdempotencyKeys:   24 * time.Hour,
			InboundEvents:     720 * time.Hour,
			RefreshTokenGrace: 2 * time.Hour,
		},
		now:    func() time.Time { return now },
		logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
}

func TestRetentionWorkerUsesEachPolicyWindow(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store := &fakeRetentionStore{cutoffs: map[string]time.Time{}, errs: map[string]error{}}

	if err := newTestRetentionWorker(store, now).Work(context.Background(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}

	want := map[string]time.Time{
		"idempotency_keys": now.Add(-24 * time.Hour),
		"refresh_tokens":   now.Add(-2 * time.Hour),
		"inbound_events":   now.Add(-720 * time.Hour),
	}
	for name, cutoff := range want {
		if got := store.cutoffs[name]; !got.Equal(cutoff) {
			t.Errorf("%s cutoff = %v, want %v", name, got, cutoff)
		}
	}
}

func TestRetentionWorkerRunsEverySweepAndReportsFailures(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeRetentionStore{
		cutoffs: map[string]time.Time{},
		errs:    map[string]error{"refresh_tokens": boom},
	}

	err := newTestRetentionWorker(store, time.Now()).Work(context.Background(), nil)
	if !errors.Is(err, boom) {
		t.Fatalf("Work error = %v, want it to wrap the failing sweep's error", err)
	}
	if _, ok := store.cutoffs["inbound_events"]; !ok {
		t.Error("a failing sweep must not stop the sweeps after it")
	}
}

func TestRetentionPeriodicJobBuilds(t *testing.T) {
	if retentionPeriodicJob() == nil {
		t.Fatal("retentionPeriodicJob returned nil")
	}
}
