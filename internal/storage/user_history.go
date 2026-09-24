package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type SessionStartRequest struct {
	ID               string  `json:"id"`
	TrackID          string  `json:"track_id"`
	ClientInstanceID string  `json:"client_instance_id"`
	QueueItemID      *string `json:"queue_item_id,omitempty"`
	SelectionToken   *string `json:"selection_token,omitempty"`
}
type SessionReportRequest struct {
	Sequence       int64   `json:"sequence"`
	ListenedMS     int64   `json:"listened_ms"`
	PositionMS     int64   `json:"position_ms"`
	DurationMS     *int64  `json:"duration_ms"`
	SeekCount      int64   `json:"seek_count"`
	TerminalReason *string `json:"terminal_reason,omitempty"`
}
type PlaybackSession struct {
	ID             string     `json:"id"`
	TrackID        string     `json:"track_id"`
	QueueItemID    *string    `json:"queue_item_id"`
	SelectionToken *string    `json:"selection_token"`
	StartedAt      time.Time  `json:"started_at"`
	MeaningfulAt   *time.Time `json:"meaningful_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	EndedAt        *time.Time `json:"ended_at"`
	Sequence       int64      `json:"sequence"`
	ListenedMS     int64      `json:"listened_ms"`
	PositionMS     int64      `json:"position_ms"`
	DurationMS     *int64     `json:"duration_ms"`
	SeekCount      int64      `json:"seek_count"`
	TerminalReason *string    `json:"terminal_reason"`
}

const sessionSelectSQL = `SELECT id::text,track_id,queue_item_id,selection_token::text,started_at,meaningful_at,completed_at,ended_at,last_sequence,listened_ms,last_position_ms,reported_duration_ms,seek_count,terminal_reason FROM playback_sessions WHERE id=$1`

func scanSession(row pgx.Row) (PlaybackSession, error) {
	var s PlaybackSession
	err := row.Scan(&s.ID, &s.TrackID, &s.QueueItemID, &s.SelectionToken, &s.StartedAt, &s.MeaningfulAt, &s.CompletedAt, &s.EndedAt, &s.Sequence, &s.ListenedMS, &s.PositionMS, &s.DurationMS, &s.SeekCount, &s.TerminalReason)
	return s, err
}

func (s *Store) StartListeningSession(ctx context.Context, req SessionStartRequest) (PlaybackSession, error) {
	req.ID = strings.ToLower(req.ID)
	req.ClientInstanceID = strings.ToLower(req.ClientInstanceID)
	if req.SelectionToken != nil {
		token := strings.ToLower(*req.SelectionToken)
		req.SelectionToken = &token
	}
	if !validUUID(req.ID) || !validUUID(req.ClientInstanceID) || !ValidCatalogID(req.TrackID, "track") || !(req.QueueItemID == nil && req.SelectionToken == nil || req.QueueItemID != nil && req.SelectionToken != nil) {
		return PlaybackSession{}, ErrUserInvalid
	}
	if req.QueueItemID != nil && (!validLogicalID(*req.QueueItemID, "qi_") || !validUUID(*req.SelectionToken)) {
		return PlaybackSession{}, ErrUserInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlaybackSession{}, err
	}
	defer tx.Rollback(ctx)
	// Client-instance lock prevents concurrent starts from leaving two open rows.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", receiptLock("session.start", req.ClientInstanceID)); err != nil {
		return PlaybackSession{}, err
	}
	var oldTrack, oldClient string
	var oldItem, oldToken *string
	err = tx.QueryRow(ctx, "SELECT track_id,client_instance_id::text,queue_item_id,selection_token::text FROM playback_sessions WHERE id=$1", req.ID).Scan(&oldTrack, &oldClient, &oldItem, &oldToken)
	if err == nil {
		if oldTrack != req.TrackID || oldClient != req.ClientInstanceID || !sameNullableString(oldItem, req.QueueItemID) || !sameNullableString(oldToken, req.SelectionToken) {
			return PlaybackSession{}, ErrIdempotencyConflict
		}
		result, e := scanSession(tx.QueryRow(ctx, sessionSelectSQL, req.ID))
		if e != nil {
			return result, e
		}
		if e = tx.Commit(ctx); e != nil {
			return PlaybackSession{}, e
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PlaybackSession{}, err
	}
	exists, err := publicTrackExists(ctx, tx, req.TrackID)
	if err != nil {
		return PlaybackSession{}, err
	}
	if !exists {
		return PlaybackSession{}, ErrUserNotFound
	}
	if req.QueueItemID != nil {
		var selected bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM active_queue aq JOIN queue_items qi ON qi.id=aq.current_item_id WHERE aq.singleton=true AND aq.selection_state='selected' AND qi.id=$1 AND aq.selection_token=$2 AND qi.track_id=$3)`, *req.QueueItemID, *req.SelectionToken, req.TrackID).Scan(&selected)
		if err != nil {
			return PlaybackSession{}, err
		}
		if !selected {
			return PlaybackSession{}, ErrStaleSelection
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE playback_sessions SET ended_at=now(),terminal_reason='interrupted' WHERE client_instance_id=$1 AND ended_at IS NULL", req.ClientInstanceID); err != nil {
		return PlaybackSession{}, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO playback_sessions(id,track_id,queue_item_id,selection_token,client_instance_id) VALUES($1,$2,$3,$4,$5)", req.ID, req.TrackID, req.QueueItemID, req.SelectionToken, req.ClientInstanceID); err != nil {
		return PlaybackSession{}, err
	}
	result, err := scanSession(tx.QueryRow(ctx, sessionSelectSQL, req.ID))
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlaybackSession{}, err
	}
	return result, nil
}

func meaningfulThreshold(duration *int64) int64 {
	if duration == nil {
		return 30000
	}
	half := *duration / 2
	return min(int64(30000), max(int64(1000), half))
}
func nearEnd(position, duration int64) bool {
	if position > duration+2000 {
		return false
	}
	allowance := max(int64(2000), min(int64(5000), duration/50))
	return duration-position <= allowance
}

func validateSessionReport(req SessionReportRequest) error {
	if req.Sequence < 1 || req.Sequence > 1_000_000_000 || req.ListenedMS < 0 || req.PositionMS < 0 || req.SeekCount < 0 || req.SeekCount > 1_000_000 || req.ListenedMS > 7*24*60*60*1000 || req.PositionMS > 7*24*60*60*1000 {
		return ErrUserInvalid
	}
	if req.DurationMS != nil && (*req.DurationMS <= 0 || *req.DurationMS > 7*24*60*60*1000 || req.PositionMS > *req.DurationMS+5000) {
		return ErrUserInvalid
	}
	if req.TerminalReason != nil {
		switch *req.TerminalReason {
		case "ended", "stopped", "decoder_error", "disconnected":
		default:
			return ErrUserInvalid
		}
	}
	return nil
}

func applySessionReportTx(ctx context.Context, tx pgx.Tx, id string, req SessionReportRequest, allowQueuedEnd bool, expectedItem, expectedToken string) (PlaybackSession, error) {
	if !validUUID(id) {
		return PlaybackSession{}, ErrUserInvalid
	}
	if err := validateSessionReport(req); err != nil {
		return PlaybackSession{}, err
	}
	current, err := scanSession(tx.QueryRow(ctx, sessionSelectSQL+" FOR UPDATE", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlaybackSession{}, ErrUserNotFound
	}
	if err != nil {
		return PlaybackSession{}, err
	}
	if req.Sequence <= current.Sequence {
		return current, nil
	}
	if current.EndedAt != nil {
		return PlaybackSession{}, ErrStaleSelection
	}
	if req.ListenedMS < current.ListenedMS || req.SeekCount < current.SeekCount {
		return PlaybackSession{}, ErrUserInvalid
	}
	serverElapsed := time.Since(current.StartedAt).Milliseconds()
	if serverElapsed < 0 {
		serverElapsed = 0
	}
	if req.ListenedMS > serverElapsed+5000 {
		return PlaybackSession{}, ErrUserInvalid
	}
	if req.TerminalReason != nil && *req.TerminalReason == "ended" {
		if current.QueueItemID != nil && (!allowQueuedEnd || expectedItem != *current.QueueItemID || current.SelectionToken == nil || expectedToken != *current.SelectionToken) {
			return PlaybackSession{}, ErrStaleSelection
		}
		if req.DurationMS != nil && !nearEnd(req.PositionMS, *req.DurationMS) {
			return PlaybackSession{}, ErrUserInvalid
		}
	}
	meaningful := req.ListenedMS >= meaningfulThreshold(req.DurationMS)
	completionListenDelta := req.ListenedMS - current.ListenedMS
	completionPositionDelta := req.PositionMS - current.PositionMS
	naturalEndProgress := req.SeekCount == current.SeekCount && completionListenDelta >= 1000 && completionPositionDelta > 0 && completionPositionDelta <= completionListenDelta+5000
	alreadyAtNaturalEnd := req.SeekCount == 0 && current.SeekCount == 0 && current.ListenedMS > 0 && current.PositionMS > 0 && req.DurationMS != nil && nearEnd(current.PositionMS, *req.DurationMS)
	completed := req.TerminalReason != nil && *req.TerminalReason == "ended" && req.DurationMS != nil && meaningful && (naturalEndProgress || alreadyAtNaturalEnd)
	var meaningfulAt, completedAt, endedAt *time.Time = current.MeaningfulAt, current.CompletedAt, current.EndedAt
	now := time.Now().UTC()
	if meaningful && meaningfulAt == nil {
		meaningfulAt = &now
	}
	if req.TerminalReason != nil {
		endedAt = &now
	}
	if completed {
		completedAt = &now
	}
	if _, err = tx.Exec(ctx, `UPDATE playback_sessions SET last_sequence=$2,listened_ms=$3,last_position_ms=$4,reported_duration_ms=$5,seek_count=$6,meaningful_at=$7,completed_at=$8,ended_at=$9,terminal_reason=$10 WHERE id=$1`, id, req.Sequence, req.ListenedMS, req.PositionMS, req.DurationMS, req.SeekCount, meaningfulAt, completedAt, endedAt, req.TerminalReason); err != nil {
		return PlaybackSession{}, err
	}
	return scanSession(tx.QueryRow(ctx, sessionSelectSQL, id))
}

func (s *Store) ReportListeningSession(ctx context.Context, id string, req SessionReportRequest) (PlaybackSession, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlaybackSession{}, err
	}
	defer tx.Rollback(ctx)
	result, err := applySessionReportTx(ctx, tx, id, req, false, "", "")
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlaybackSession{}, err
	}
	return result, nil
}

type HistoryItem struct {
	PlaybackSession
	Title        *string `json:"title"`
	ArtistCredit *string `json:"artist_credit"`
	Available    bool    `json:"available"`
}

func (s *Store) ListHistory(ctx context.Context, limit int, beforeTime *time.Time, beforeID string) ([]HistoryItem, error) {
	var id *string
	if beforeTime != nil {
		id = &beforeID
	}
	rows, err := s.pool.Query(ctx, `SELECT ps.id::text,ps.track_id,ps.queue_item_id,ps.selection_token::text,ps.started_at,ps.meaningful_at,ps.completed_at,ps.ended_at,ps.last_sequence,ps.listened_ms,ps.last_position_ms,ps.reported_duration_ms,ps.seek_count,ps.terminal_reason,t.title,t.artist_credit,EXISTS(SELECT 1 FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE mo.track_id=ps.track_id AND ml.availability='available' AND lr.enabled) FROM playback_sessions ps JOIN tracks t ON t.id=ps.track_id WHERE (ps.meaningful_at IS NOT NULL OR ps.completed_at IS NOT NULL) AND ($1::timestamptz IS NULL OR (ps.started_at,ps.id)<($1,$2::uuid)) ORDER BY ps.started_at DESC,ps.id DESC LIMIT $3`, beforeTime, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryItem{}
	for rows.Next() {
		var x HistoryItem
		if err := rows.Scan(&x.ID, &x.TrackID, &x.QueueItemID, &x.SelectionToken, &x.StartedAt, &x.MeaningfulAt, &x.CompletedAt, &x.EndedAt, &x.Sequence, &x.ListenedMS, &x.PositionMS, &x.DurationMS, &x.SeekCount, &x.TerminalReason, &x.Title, &x.ArtistCredit, &x.Available); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
