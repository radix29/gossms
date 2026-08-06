package activity

import (
	"context"
	"database/sql"
)

// SessionStats is the current session and request picture. Only
// ActiveRequests is drawn today; the other four are collected for the
// Sessions tab in increment 2 and cost nothing extra, since all five come
// from one query.
type SessionStats struct {
	UserSessions     float64
	ActiveRequests   float64
	RunnableRequests float64
	SuspendedTasks   float64
	BlockedRequests  float64
}

// "User session" means is_user_process = 1 throughout, for the request
// counts as much as for the session count — a session_id > 50 cut-off is
// the conventional shorthand for the same thing but disagrees with it at
// the edges, and two definitions under one heading make the request counts
// look wrong against the session count. The request counts additionally
// exclude the connection asking, which is always running and would
// otherwise make an idle server look permanently busy.
const sessionQuery = `
SELECT
  COUNT(DISTINCT s.session_id),
  COUNT(r.session_id),
  ISNULL(SUM(CASE WHEN r.status = 'runnable' THEN 1 ELSE 0 END), 0),
  ISNULL(SUM(CASE WHEN r.status = 'suspended' THEN 1 ELSE 0 END), 0),
  ISNULL(SUM(CASE WHEN r.blocking_session_id <> 0 THEN 1 ELSE 0 END), 0)
FROM sys.dm_exec_sessions AS s
LEFT JOIN sys.dm_exec_requests AS r
  ON r.session_id = s.session_id AND r.session_id <> @@SPID
WHERE s.is_user_process = 1`

func collectSessions(ctx context.Context, db *sql.DB) (SessionStats, error) {
	var s SessionStats
	err := db.QueryRowContext(ctx, sessionQuery).Scan(
		&s.UserSessions, &s.ActiveRequests, &s.RunnableRequests, &s.SuspendedTasks, &s.BlockedRequests)
	if err != nil {
		return SessionStats{}, err
	}
	return s, nil
}
