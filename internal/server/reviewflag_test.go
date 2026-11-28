package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFlagCacheServesWithinTTLThenReloads(t *testing.T) {
	clk := &clock{t: time.Unix(1000, 0)}
	c := flagCache{ttl: 30 * time.Second, now: clk.now}
	value, loads := true, 0
	load := func(context.Context) (bool, error) { loads++; return value, nil }

	for i := 0; i < 3; i++ {
		if got, err := c.enabled(context.Background(), load); err != nil || !got {
			t.Fatalf("read %d: %v %v", i, got, err)
		}
	}
	if loads != 1 {
		t.Fatalf("loads within ttl: %d", loads)
	}
	value = false
	clk.t = clk.t.Add(29 * time.Second)
	if got, _ := c.enabled(context.Background(), load); !got || loads != 1 {
		t.Fatalf("at 29s: %v loads=%d", got, loads)
	}
	clk.t = clk.t.Add(2 * time.Second)
	if got, err := c.enabled(context.Background(), load); err != nil || got || loads != 2 {
		t.Fatalf("at 31s: %v %v loads=%d", got, err, loads)
	}
}

func TestFlagCacheNeverCachesAnError(t *testing.T) {
	clk := &clock{t: time.Unix(1000, 0)}
	c := flagCache{ttl: 30 * time.Second, now: clk.now}
	fail := true
	loads := 0
	load := func(context.Context) (bool, error) {
		loads++
		if fail {
			return false, errors.New("db down")
		}
		return true, nil
	}
	if _, err := c.enabled(context.Background(), load); err == nil {
		t.Fatal("want the error")
	}
	fail = false
	if got, err := c.enabled(context.Background(), load); err != nil || !got || loads != 2 {
		t.Fatalf("after recovery: %v %v loads=%d", got, err, loads)
	}
}

func TestFlagCacheZeroTTLReadsEveryTime(t *testing.T) {
	c := flagCache{}
	loads := 0
	load := func(context.Context) (bool, error) { loads++; return true, nil }
	for i := 0; i < 3; i++ {
		if _, err := c.enabled(context.Background(), load); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 3 {
		t.Fatalf("loads: %d", loads)
	}
}

func TestParseFlagCacheTTL(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 30 * time.Second, "0": 0, "5": 5 * time.Second, "300": 300 * time.Second} {
		if got, err := ParseFlagCacheTTL(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"-1", "x", "301", "1.5"} {
		if _, err := ParseFlagCacheTTL(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}
