package submission

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func Backoff(failures int) time.Duration {
	if failures <= 0 {
		return 2 * time.Second
	}
	if failures >= 4 {
		return 30 * time.Second
	}
	return time.Duration(2<<failures) * time.Second
}
func (s *Service) Watch(ctx context.Context, submissionURL string, options WatchOptions, emit func(WatchEvent)) (Result, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	interval := options.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var latest Result
	failures := 0
	for {
		result, err := s.FetchResult(ctx, submissionURL)
		delay := interval
		if err == nil {
			failures = 0
			latest = result
			if s.Store != nil {
				if e := s.Store.SaveResult(result); e != nil {
					return latest, e
				}
			}
			if emit != nil {
				emit(WatchEvent{Result: &result})
			}
			if result.Terminal() {
				return latest, nil
			}
		} else {
			if ctx.Err() != nil {
				return latest, ctx.Err()
			}
			if errors.Is(err, ErrAuth) {
				return latest, err
			}
			failures++
			delay = Backoff(failures)
			if failures >= 5 {
				paused := fmt.Errorf("%w: %v", ErrPaused, err)
				if emit != nil {
					emit(WatchEvent{Err: paused})
				}
				return latest, paused
			}
			if emit != nil {
				emit(WatchEvent{Err: err, Next: delay})
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return latest, ctx.Err()
		case <-timer.C:
		}
	}
}
