package storage

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const contextWindow = 128
const contextHistory = 100
const contextLowWater = 24

type PlaybackSource struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id,omitempty"`
	Order    string   `json:"order"`
	Query    string   `json:"query,omitempty"`
	TrackIDs []string `json:"track_ids,omitempty"`
}
type ContextStartRequest struct {
	Source          PlaybackSource `json:"source"`
	StartTrackID    string         `json:"start_track_id,omitempty"`
	StartItemID     string         `json:"start_item_id,omitempty"`
	Shuffle         bool           `json:"shuffle"`
	ExpectedVersion int64          `json:"expected_version"`
	SourceVersion   *int64         `json:"source_version,omitempty"`
}
type QueueContext struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Shuffled bool   `json:"shuffled"`
	Total    int64  `json:"total"`
	More     bool   `json:"more"`
	Previous bool   `json:"previous"`
	Round    int64  `json:"round"`
}

func readQueueContext(ctx context.Context, q queueQueryer, current *string) (*QueueContext, error) {
	var c QueueContext
	err := q.QueryRow(ctx, `SELECT c.id::text,c.source_kind,c.source_name,c.shuffled,c.total,NOT c.exhausted,c.round,
 EXISTS(SELECT 1 FROM queue_context_items ci JOIN queue_context_tracks m ON m.ordinal=ci.ordinal WHERE ci.queue_item_id=$1 AND m.play_rank>0)
 FROM queue_playback_context c WHERE singleton=true`, current).Scan(&c.ID, &c.Kind, &c.Name, &c.Shuffled, &c.Total, &c.More, &c.Round, &c.Previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}
func clearQueueContext(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "DELETE FROM queue_playback_context")
	return err
}

const contextPlayableCTE = `WITH playable AS MATERIALIZED (SELECT DISTINCT mo.track_id FROM media_objects mo JOIN media_locations ml ON ml.media_object_id=mo.id JOIN library_roots lr ON lr.id=ml.root_id WHERE ml.availability='available' AND lr.enabled) `

func validPlaybackSource(s PlaybackSource) bool {
	if len(s.Query) > 512 || len([]rune(s.Query)) > 120 || !utf8.ValidString(s.Query) || strings.ContainsRune(s.Query, 0) {
		return false
	}
	if s.Order != "original" && s.Order != "title" && s.Order != "title_desc" && s.Order != "artist" && s.Order != "album" && s.Order != "recent" {
		return false
	}
	switch s.Kind {
	case "library", "favorites":
		return s.ID == "" && len(s.TrackIDs) == 0
	case "artist", "album":
		return ValidCatalogID(s.ID, s.Kind) && len(s.TrackIDs) == 0
	case "playlist":
		return validLogicalID(s.ID, "pl_") && len(s.TrackIDs) == 0
	case "selection":
		if s.ID != "" || len(s.TrackIDs) < 1 || len(s.TrackIDs) > 5000 {
			return false
		}
		for _, id := range s.TrackIDs {
			if !ValidCatalogID(id, "track") {
				return false
			}
		}
		return true
	}
	return false
}

// Ordering is assembled only from these fixed expressions. All source/query
// input stays in parameters. Source members, never encoded objects, are copied.
func snapshotPlaybackSource(ctx context.Context, tx pgx.Tx, req ContextStartRequest) (string, error) {
	s := req.Source
	from, filter, original, item, name := "tracks t", "", "coalesce(t.catalog_title_key,'')", "NULL::text", "Library"
	args := []any{s.ID, s.Query}
	switch s.Kind {
	case "artist":
		filter += ` AND EXISTS(SELECT 1 FROM track_artist_memberships am WHERE am.track_id=t.id AND am.artist_id=$1)`
		if err := tx.QueryRow(ctx, "SELECT display_credit FROM catalog_artists WHERE id=$1", s.ID).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", ErrUserNotFound
			}
			return "", err
		}
	case "album":
		filter += ` AND EXISTS(SELECT 1 FROM track_album_memberships am WHERE am.track_id=t.id AND am.album_id=$1)`
		original = albumSortExpression
		if err := tx.QueryRow(ctx, "SELECT display_title FROM catalog_albums WHERE id=$1", s.ID).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", ErrUserNotFound
			}
			return "", err
		}
	case "playlist":
		playlist, err := lockPlaylist(ctx, tx, s.ID)
		if err != nil {
			return "", err
		}
		if req.SourceVersion == nil || playlist.Revision != *req.SourceVersion {
			return "", ErrStaleVersion
		}
		name = playlist.Name
		from = "playlist_items pi JOIN tracks t ON t.id=pi.track_id"
		filter = " AND pi.playlist_id=$1"
		original = "lpad(pi.position::text,16,'0')"
		item = "pi.id"
	case "favorites":
		name = "Favorites"
		from = "favorite_tracks ft JOIN tracks t ON t.id=ft.track_id"
		original = "to_char(ft.created_at AT TIME ZONE 'UTC','YYYYMMDDHH24MISSUS') DESC"
		filter = ""
	case "selection":
		name = "Selected songs"
		from = "unnest($3::text[]) WITH ORDINALITY AS picked(track_id,position) JOIN tracks t ON t.id=picked.track_id"
		original = "lpad(picked.position::text,16,'0')"
		filter = ""
		args = append(args, s.TrackIDs)
		unique := map[string]bool{}
		for _, id := range s.TrackIDs {
			unique[id] = true
		}
		var known int
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT t.id) FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id WHERE t.id=ANY($1::text[]) AND ml.root_id IS NOT NULL`, s.TrackIDs).Scan(&known); err != nil {
			return "", err
		}
		if known != len(unique) {
			return "", ErrUserNotFound
		}
	}
	order := original
	switch s.Order {
	case "title":
		order = "coalesce(t.catalog_title_key,'')"
	case "title_desc":
		order = "coalesce(t.catalog_title_key,'') DESC"
	case "artist":
		order = "lower(coalesce(t.artist_credit,'Unknown artist')),coalesce(t.catalog_title_key,'')"
	case "album":
		order = "lower(coalesce(t.album_title,'')),coalesce(t.catalog_title_key,'')"
	case "recent":
		timeColumn := "t.created_at"
		if s.Kind == "playlist" {
			timeColumn = "pi.created_at"
		}
		if s.Kind == "favorites" {
			timeColumn = "ft.created_at"
		}
		order = timeColumn + " DESC"
	}
	if s.Kind == "playlist" {
		titleExpr := `lower(normalize(coalesce(NULLIF(t.title,''),'Untitled track'),NFC)) COLLATE "C"`
		switch s.Order {
		case "title":
			order = titleExpr
		case "title_desc":
			order = titleExpr + " DESC"
		case "artist":
			order = `lower(normalize(coalesce(NULLIF(t.artist_credit,''),'Unknown artist'),NFC)) COLLATE "C",` + titleExpr
		case "album":
			order = `lower(normalize(coalesce(t.album_title,''),NFC)) COLLATE "C",` + titleExpr
		}
	}
	tie := "t.id"
	if s.Kind == "favorites" && (s.Order == "original" || s.Order == "recent") {
		tie = "t.id DESC"
	}
	if s.Kind == "playlist" {
		tie = "pi.position,pi.id"
	}
	if s.Kind == "selection" {
		tie += " ,picked.position"
	}
	if s.Order == "title_desc" {
		tie = "t.id DESC"
		if s.Kind == "playlist" {
			tie = "pi.position,pi.id"
		}
	}
	query := contextPlayableCTE + `INSERT INTO queue_context_tracks(ordinal,track_id,source_item_id,play_rank)
 SELECT row_number() OVER(ORDER BY ` + order + `,` + tie + `)-1,t.id,` + item + `,row_number() OVER(ORDER BY ` + order + `,` + tie + `)-1
	 FROM ` + from + ` WHERE t.id IN (SELECT track_id FROM playable)` + filter + `
 AND ($2='' OR strpos(lower(normalize(coalesce(NULLIF(t.title,''),'Untitled track'),NFC)),lower(normalize($2,NFC)))>0 OR strpos(lower(normalize(coalesce(NULLIF(t.artist_credit,''),'Unknown artist'),NFC)),lower(normalize($2,NFC)))>0 OR strpos(lower(normalize(coalesce(t.album_title,''),NFC)),lower(normalize($2,NFC)))>0)`
	// Parameter 1 belongs to scoped sources; cast it in the unscoped predicate
	// too so PostgreSQL can determine its type without interpreting SQL input.
	query += ` AND ($1::text IS NOT NULL)`
	_, err := tx.Exec(ctx, query, args...)
	return name, err
}
func (s *Store) StartQueueContext(ctx context.Context, key string, req ContextStartRequest) (MutationResult, error) {
	if req.ExpectedVersion < 0 || !validPlaybackSource(req.Source) || req.StartTrackID != "" && !ValidCatalogID(req.StartTrackID, "track") || req.StartItemID != "" && !validLogicalID(req.StartItemID, "pi_") || req.StartItemID != "" && req.Source.Kind != "playlist" {
		return MutationResult{}, ErrUserInvalid
	}
	return s.mutateWithReceipt(ctx, "queue.context.start", key, req, func(tx pgx.Tx) (int, any, error) {
		q, err := lockQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if q.Revision != req.ExpectedVersion {
			return 0, nil, ErrStaleVersion
		}
		if err = clearQueueContext(ctx, tx); err != nil {
			return 0, nil, err
		}
		id, err := newUUID()
		if err != nil {
			return 0, nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO queue_playback_context(singleton,id,source_kind,source_id,source_name,source_order,source_query,shuffled,total) VALUES(true,$1,$2,NULLIF($3,''),'Library',$4,$5,$6,0)`, id, req.Source.Kind, req.Source.ID, req.Source.Order, req.Source.Query, req.Shuffle)
		if err != nil {
			return 0, nil, err
		}
		name, err := snapshotPlaybackSource(ctx, tx, req)
		if err != nil {
			return 0, nil, err
		}
		// Name is a compact presentation label; long tags never bloat a receipt.
		if len(name) > 512 {
			cut := 0
			for at, r := range name {
				if at+utf8.RuneLen(r) > 512 {
					break
				}
				cut = at + utf8.RuneLen(r)
			}
			name = name[:cut]
		}
		var total, start int64
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM queue_context_tracks").Scan(&total); err != nil {
			return 0, nil, err
		}
		if total == 0 {
			return 0, nil, ErrUserNotFound
		}
		err = tx.QueryRow(ctx, `SELECT ordinal FROM queue_context_tracks WHERE ($1='' OR track_id=$1) AND ($2='' OR source_item_id=$2) ORDER BY ordinal LIMIT 1`, req.StartTrackID, req.StartItemID).Scan(&start)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, ErrUserNotFound
		}
		if err != nil {
			return 0, nil, err
		}
		if req.Shuffle {
			_, err = tx.Exec(ctx, `UPDATE queue_context_tracks m SET play_rank=r.rank FROM (SELECT ordinal,row_number() OVER(ORDER BY CASE WHEN ordinal=$1 THEN 0 ELSE 1 END,random(),ordinal)-1 rank FROM queue_context_tracks) r WHERE m.ordinal=r.ordinal`, start)
			if err != nil {
				return 0, nil, err
			}
			start = 0
		}
		if _, err = tx.Exec(ctx, `UPDATE queue_playback_context SET source_name=$1,total=$2,next_rank=$3`, name, total, start); err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM queue_items"); err != nil {
			return 0, nil, err
		}
		if err = fillQueueContext(ctx, tx, contextWindow, -1, false); err != nil {
			return 0, nil, err
		}
		full, err := readQueue(ctx, tx)
		if err != nil {
			return 0, nil, err
		}
		if len(full.Items) == 0 {
			return 0, nil, ErrUserNotFound
		}
		token, err := newUUID()
		if err != nil {
			return 0, nil, err
		}
		q.Revision++
		q.CurrentItemID = &full.Items[0].ID
		q.SelectionToken = &token
		q.SelectionState = "selected"
		if err = setQueueSelection(ctx, tx, q.Revision, q.CurrentItemID, q.SelectionToken, q.SelectionState); err != nil {
			return 0, nil, err
		}
		q.Context, err = readQueueContext(ctx, tx, q.CurrentItemID)
		if err != nil {
			return 0, nil, err
		}
		return 201, queueChange(q), nil
	})
}

type contextMember struct {
	ordinal, rank int64
	track         string
}

func fillQueueContext(ctx context.Context, tx pgx.Tx, limit int, before int64, wrap bool) error {
	if limit <= 0 {
		return nil
	}
	var cursor, round int64
	if err := tx.QueryRow(ctx, `SELECT next_rank,round FROM queue_playback_context WHERE singleton=true`).Scan(&cursor, &round); err != nil {
		return err
	}
	direction, condition := "", "m.play_rank >= $1 AND NOT m.seen AND NOT m.excluded"
	if before >= 0 {
		cursor = before
		condition = "m.play_rank < $1"
		direction = " DESC"
	}
	if wrap {
		cursor = 1 << 62
	}
	rows, err := tx.Query(ctx, contextPlayableCTE+`SELECT m.ordinal,m.play_rank,m.track_id FROM queue_context_tracks m WHERE `+condition+` AND NOT m.failed AND m.track_id IN (SELECT track_id FROM playable) ORDER BY m.play_rank`+direction+` LIMIT $2`, cursor, limit)
	if err != nil {
		return err
	}
	members := []contextMember{}
	for rows.Next() {
		var m contextMember
		if err = rows.Scan(&m.ordinal, &m.rank, &m.track); err != nil {
			rows.Close()
			return err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if before >= 0 {
		for i, j := 0, len(members)-1; i < j; i, j = i+1, j-1 {
			members[i], members[j] = members[j], members[i]
		}
	}
	full, err := readQueue(ctx, tx)
	if err != nil {
		return err
	}
	if len(full.Items)+len(members) > 1000 {
		return ErrUserLimit
	}
	additions := make([]QueueItem, 0, len(members))
	ids, tracks, ordinals := make([]string, 0, len(members)), make([]string, 0, len(members)), make([]int64, 0, len(members))
	for _, m := range members {
		id, e := newLogicalID("qi_")
		if e != nil {
			return e
		}
		ids = append(ids, id)
		tracks = append(tracks, m.track)
		ordinals = append(ordinals, m.ordinal)
		additions = append(additions, QueueItem{ID: id, TrackID: m.track})
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO queue_items(id,track_id,position) SELECT id,track_id,$3+ordinality-1 FROM unnest($1::text[],$2::text[]) WITH ORDINALITY AS added(id,track_id,ordinality)`, ids, tracks, len(full.Items)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO queue_context_items(queue_item_id,ordinal,round) SELECT id,ordinal,$3 FROM unnest($1::text[],$2::bigint[]) AS added(id,ordinal)`, ids, ordinals, round); err != nil {
			return err
		}
	}
	if before >= 0 {
		combined := append(additions, full.Items...)
		return persistQueueOrder(ctx, tx, combined)
	}
	if len(members) > 0 {
		cursor = members[len(members)-1].rank + 1
	}
	var more bool
	if err = tx.QueryRow(ctx, contextPlayableCTE+`SELECT EXISTS(SELECT 1 FROM queue_context_tracks m WHERE m.play_rank>=$1 AND NOT m.seen AND NOT m.excluded AND NOT m.failed AND m.track_id IN (SELECT track_id FROM playable))`, cursor).Scan(&more); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE queue_playback_context SET next_rank=$1,exhausted=$2 WHERE singleton=true`, cursor, !more)
	return err
}

// Preparation happens inside queue.advance's receipt transaction, after final
// report validation. It never changes the live selection token on its own.
func prepareContextAdvance(ctx context.Context, tx pgx.Tx, q QueueSnapshot, req QueueAdvanceRequest) (QueueSnapshot, error) {
	if q.Context == nil {
		return q, nil
	}
	at := currentIndex(q.Items, q.CurrentItemID)
	if at < 0 {
		return q, nil
	}
	if req.Direction == "previous" {
		if at > 0 {
			return q, nil
		}
		var rank int64
		err := tx.QueryRow(ctx, `SELECT m.play_rank FROM queue_context_items ci JOIN queue_context_tracks m ON m.ordinal=ci.ordinal WHERE ci.queue_item_id=$1`, q.CurrentItemID).Scan(&rank)
		if errors.Is(err, pgx.ErrNoRows) {
			return q, nil
		}
		if err != nil {
			return q, err
		}
		if rank > 0 || req.Repeat == "all" {
			if len(q.Items) == 1000 {
				return q, ErrUserLimit
			}
			if err = fillQueueContext(ctx, tx, min(contextHistory, 1000-len(q.Items)), rank, rank == 0); err != nil {
				return q, err
			}
		}
		return readQueue(ctx, tx)
	}
	if req.Direction == "ended" && req.Repeat == "one" && q.Items[at].Available {
		return q, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE queue_context_tracks m SET seen=true FROM queue_context_items ci,queue_playback_context c WHERE ci.queue_item_id=$1 AND ci.ordinal=m.ordinal AND ci.round=c.round`, q.CurrentItemID); err != nil {
		return q, err
	}
	if req.FailureCode != nil {
		if _, err := tx.Exec(ctx, `UPDATE queue_context_tracks SET failed=true WHERE track_id=$1`, q.Items[at].TrackID); err != nil {
			return q, err
		}
	}
	if at > contextHistory {
		ids := make([]string, at-contextHistory)
		for i := range ids {
			ids[i] = q.Items[i].ID
		}
		if _, err := tx.Exec(ctx, `DELETE FROM queue_items WHERE id=ANY($1::text[])`, ids); err != nil {
			return q, err
		}
		q.Items = q.Items[len(ids):]
		at -= len(ids)
		if err := persistQueueOrder(ctx, tx, q.Items); err != nil {
			return q, err
		}
	}
	upcoming := 0
	for _, item := range q.Items[at+1:] {
		if item.Available {
			upcoming++
		}
	}
	if upcoming < contextLowWater {
		if err := fillQueueContext(ctx, tx, min(contextWindow-upcoming, 1000-len(q.Items)), -1, false); err != nil {
			return q, err
		}
	}
	q, err := readQueue(ctx, tx)
	if err != nil {
		return q, err
	}
	at = currentIndex(q.Items, q.CurrentItemID)
	next := false
	for _, item := range q.Items[at+1:] {
		if item.Available {
			next = true
		}
	}
	if !next && req.Repeat == "all" {
		if _, err = tx.Exec(ctx, `UPDATE queue_context_tracks SET seen=false,excluded=false`); err != nil {
			return q, err
		}
		if req.Reshuffle || q.Context.Shuffled {
			_, err = tx.Exec(ctx, `UPDATE queue_context_tracks m SET play_rank=r.rank FROM (SELECT ordinal,row_number() OVER(ORDER BY CASE WHEN track_id=$1 THEN 1 ELSE 0 END,random(),ordinal)-1 rank FROM queue_context_tracks) r WHERE m.ordinal=r.ordinal`, q.Items[at].TrackID)
			if err != nil {
				return q, err
			}
		} else {
			if _, err = tx.Exec(ctx, `UPDATE queue_context_tracks SET play_rank=ordinal`); err != nil {
				return q, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE queue_playback_context SET next_rank=0,exhausted=false,round=round+1`); err != nil {
			return q, err
		}
		if err = fillQueueContext(ctx, tx, min(contextWindow, 1000-len(q.Items)), -1, false); err != nil {
			return q, err
		}
		return readQueue(ctx, tx)
	}
	return q, nil
}
