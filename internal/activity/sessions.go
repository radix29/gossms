package activity

import "context"

// SessionStats is the current session and request picture, from gosmo's
// RequestActivity (which defines each count). Only ActiveRequests is drawn;
// the rest are for the increment-2 Sessions tab.
type SessionStats struct {
	UserSessions     float64
	ActiveRequests   float64
	RunnableRequests float64
	SuspendedTasks   float64
	BlockedRequests  float64
}

func collectSessions(ctx context.Context, src Source) (SessionStats, error) {
	a, err := src.RequestActivity(ctx)
	if err != nil {
		return SessionStats{}, err
	}
	return SessionStats{
		UserSessions:     float64(a.UserSessions),
		ActiveRequests:   float64(a.ActiveRequests),
		RunnableRequests: float64(a.RunnableRequests),
		SuspendedTasks:   float64(a.SuspendedRequests),
		BlockedRequests:  float64(a.BlockedRequests),
	}, nil
}
