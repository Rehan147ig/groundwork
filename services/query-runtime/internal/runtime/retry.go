package runtime

import (
	"context"
	"math/rand"
	"time"
)

type retryConfig struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

func retryWithBackoff(ctx context.Context, cfg retryConfig, fn func() error) error {
	if cfg.Attempts <= 0 {
		cfg.Attempts = 3
	}
	if cfg.Base <= 0 {
		cfg.Base = 50 * time.Millisecond
	}
	if cfg.Max <= 0 {
		cfg.Max = 500 * time.Millisecond
	}
	var last error
	for attempt := 0; attempt < cfg.Attempts; attempt++ {
		if err := fn(); err != nil {
			last = err
			if attempt == cfg.Attempts-1 {
				break
			}
			delay := cfg.Base << attempt
			if delay > cfg.Max {
				delay = cfg.Max
			}
			jitter := time.Duration(rand.Int63n(int64(delay / 2)))
			timer := time.NewTimer(delay + jitter)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return nil
	}
	return last
}
