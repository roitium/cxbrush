package server

import (
	"context"
	"testing"
	"time"
)

func TestClampWorkers(t *testing.T) {
	cases := map[int]int{-5: 8, 0: 8, 1: 1, 8: 8, 32: 32, 99: 32}
	for in, want := range cases {
		if got := clampWorkers(in); got != want {
			t.Errorf("clampWorkers(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestNewRunnerCeiling(t *testing.T) {
	if got := NewRunner(nil, "", nil, 3).Ceiling(); got != 3 {
		t.Fatalf("Ceiling() = %d, want 3", got)
	}
	if got := NewRunner(nil, "", nil, 0).Ceiling(); got != 8 {
		t.Fatalf("默认 Ceiling() = %d, want 8", got)
	}
}

func TestUserGate(t *testing.T) {
	g := &userGate{limit: 2}
	ctx := context.Background()
	if !g.acquire(ctx) || !g.acquire(ctx) {
		t.Fatal("前两个槽位应该拿到")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
	defer cancel()
	if g.acquire(waitCtx) {
		t.Fatal("超过 limit 不应拿到槽位")
	}
	g.release()
	if !g.acquire(ctx) {
		t.Fatal("释放后应能拿到槽位")
	}
	g.setLimit(3)
	if !g.acquire(ctx) {
		t.Fatal("提高 limit 后应能拿到槽位")
	}
}
