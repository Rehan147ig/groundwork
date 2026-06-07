package aclsync

import "context"

// RunOnce performs a single full sync regardless of the configured Mode and
// returns when that sync completes (or fails terminally). It is a thin
// convenience wrapper around Run with Mode == ModeOnce, used by the
// cmd/msgraph-connector binary and any other one-shot caller.
//
// The original Mode is restored after the call so a Service can be reused
// for both one-shot and watch-mode runs in the same process if needed.
func (s *Service) RunOnce(ctx context.Context) error {
	saved := s.Config.Mode
	s.Config.Mode = ModeOnce
	defer func() { s.Config.Mode = saved }()
	return s.Run(ctx)
}
