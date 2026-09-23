package storage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const groupingRuleVersion = 2

var ErrGroupingIncomplete = errors.New("catalog grouping backfill incomplete")

func groupKey(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(strings.Join(strings.Fields(value), " "))))
}

type groupingTrack struct {
	id, sourceID, rootID, relativePath                    string
	title                                                 *string
	artist, album, albumArtist                            *string
	year                                                  *int
	albumScope, albumEvidence, creditKey, normalizedAlbum string
}

func nonempty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func albumEvidence(relative string) (string, string) {
	dir := path.Dir(strings.ReplaceAll(relative, "\\", "/"))
	if dir == "." {
		return "", ""
	}
	base := groupKey(path.Base(dir))
	if base == "disc 1" || base == "disc 2" || base == "disc 3" || base == "disc 4" || base == "cd1" || base == "cd2" || base == "cd3" || base == "cd4" {
		parent := path.Dir(dir)
		if parent != "." {
			return parent, "disc_parent"
		}
	}
	return dir, "folder"
}

// BackfillGrouping runs after schema migration. A failed or interrupted rebuild
// leaves the completion marker false; retry is safe and keeps existing IDs.
func (s *Store) BackfillGrouping(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return err
	}
	if err = rebuildGrouping(ctx, tx, nil); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE catalog_grouping_state SET rule_version=$1,completed=true,completed_at=now() WHERE singleton=true", groupingRuleVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrGroupingIncomplete
	}
	return tx.Commit(ctx)
}

func (s *Store) GroupingReady(ctx context.Context) error {
	var complete bool
	var version int
	if err := s.pool.QueryRow(ctx, "SELECT completed,rule_version FROM catalog_grouping_state WHERE singleton=true").Scan(&complete, &version); err != nil {
		return ErrGroupingIncomplete
	}
	if !complete || version != groupingRuleVersion {
		return ErrGroupingIncomplete
	}
	return nil
}

func rebuildGrouping(ctx context.Context, tx pgx.Tx, affected map[string]bool) error {
	rows, err := tx.Query(ctx, `SELECT t.id,t.title,t.artist_credit,t.album_title,t.album_artist_credit,t.release_year,
		ml.id,ml.root_id::text,ml.relative_path
		FROM tracks t JOIN media_locations ml ON ml.id=t.metadata_source_location_id
		WHERE ml.root_id IS NOT NULL ORDER BY t.id`)
	if err != nil {
		return err
	}
	tracks := []groupingTrack{}
	for rows.Next() {
		var t groupingTrack
		if err := rows.Scan(&t.id, &t.title, &t.artist, &t.album, &t.albumArtist, &t.year, &t.sourceID, &t.rootID, &t.relativePath); err != nil {
			rows.Close()
			return err
		}
		t.albumScope, t.albumEvidence = albumEvidence(t.relativePath)
		t.normalizedAlbum = groupKey(nonempty(t.album))
		t.creditKey = groupKey(nonempty(t.albumArtist))
		if t.creditKey == "" {
			t.creditKey = groupKey(nonempty(t.artist))
		}
		tracks = append(tracks, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	byID := make(map[string]groupingTrack, len(tracks))
	for _, t := range tracks {
		byID[t.id] = t
	}
	// Missing years join a known year only when the evidence has one candidate.
	years := map[string]map[int]bool{}
	for _, t := range tracks {
		if t.normalizedAlbum == "" || t.creditKey == "" || t.albumScope == "" || t.year == nil {
			continue
		}
		key := t.rootID + "\x00" + t.albumScope + "\x00" + t.normalizedAlbum + "\x00" + t.creditKey
		if years[key] == nil {
			years[key] = map[int]bool{}
		}
		years[key][*t.year] = true
	}
	if affected != nil {
		oldIDs := make([]string, 0, len(affected))
		for id := range affected {
			oldIDs = append(oldIDs, id)
		}
		for _, id := range oldIDs {
			peers, e := tx.Query(ctx, `SELECT m2.track_id FROM track_album_memberships m1 JOIN track_album_memberships m2 ON m2.album_id=m1.album_id WHERE m1.track_id=$1`, id)
			if e != nil {
				return e
			}
			for peers.Next() {
				var peer string
				if e = peers.Scan(&peer); e != nil {
					peers.Close()
					return e
				}
				affected[peer] = true
			}
			e = peers.Err()
			peers.Close()
			if e != nil {
				return e
			}
		}
		bases := map[string]bool{}
		for id := range affected {
			if t, ok := byID[id]; ok {
				bases[t.rootID+"\x00"+t.albumScope+"\x00"+t.normalizedAlbum+"\x00"+t.creditKey] = true
			}
		}
		for _, t := range tracks {
			if bases[t.rootID+"\x00"+t.albumScope+"\x00"+t.normalizedAlbum+"\x00"+t.creditKey] {
				affected[t.id] = true
			}
		}
	}
	for _, t := range tracks {
		if affected != nil && !affected[t.id] {
			continue
		}
		if _, err = tx.Exec(ctx, "UPDATE tracks SET catalog_title_key=$2 WHERE id=$1 AND catalog_title_key IS DISTINCT FROM $2", t.id, groupKey(nonempty(t.title))); err != nil {
			return err
		}
		var albumID string
		if key, year := desiredAlbum(t, years); key != "" {
			var albumArtistID *string
			if nonempty(t.albumArtist) != "" {
				id, e := ensureArtist(ctx, tx, t, nonempty(t.albumArtist), "album_artist_credit", t.albumScope, byID)
				if e != nil {
					return e
				}
				albumArtistID = &id
			}
			id, e := ensureAlbum(ctx, tx, t, year, albumArtistID, byID, years)
			if e != nil {
				return e
			}
			albumID = id
		}
		if albumID != "" {
			_, err = tx.Exec(ctx, `INSERT INTO track_album_memberships(track_id,album_id,source_location_id,raw_album_title,raw_album_artist_credit,raw_track_artist_credit,raw_release_year,evidence_code,rule_version)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(track_id) DO UPDATE SET album_id=excluded.album_id,source_location_id=excluded.source_location_id,raw_album_title=excluded.raw_album_title,raw_album_artist_credit=excluded.raw_album_artist_credit,raw_track_artist_credit=excluded.raw_track_artist_credit,raw_release_year=excluded.raw_release_year,evidence_code=excluded.evidence_code,rule_version=excluded.rule_version`, t.id, albumID, t.sourceID, nonempty(t.album), t.albumArtist, t.artist, t.year, t.albumEvidence, groupingRuleVersion)
			if err != nil {
				return err
			}
		} else {
			if _, err = tx.Exec(ctx, "DELETE FROM track_album_memberships WHERE track_id=$1", t.id); err != nil {
				return err
			}
		}
		roles := []struct {
			credit *string
			role   string
		}{{t.artist, "track_credit"}, {t.albumArtist, "album_artist_credit"}}
		for _, role := range roles {
			credit := nonempty(role.credit)
			if credit == "" {
				if _, err = tx.Exec(ctx, "DELETE FROM track_artist_memberships WHERE track_id=$1 AND role=$2", t.id, role.role); err != nil {
					return err
				}
				continue
			}
			id, e := ensureArtist(ctx, tx, t, credit, role.role, t.albumScope, byID)
			if e != nil {
				return e
			}
			code, _ := artistScope(t, t.albumScope, credit)
			_, err = tx.Exec(ctx, `INSERT INTO track_artist_memberships(track_id,artist_id,role,source_location_id,raw_credit,evidence_code,rule_version)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(track_id,role) DO UPDATE SET artist_id=excluded.artist_id,source_location_id=excluded.source_location_id,raw_credit=excluded.raw_credit,evidence_code=excluded.evidence_code,rule_version=excluded.rule_version`, t.id, id, role.role, t.sourceID, credit, code, groupingRuleVersion)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func artistScope(t groupingTrack, albumScope, credit string) (string, string) {
	if albumScope != "" {
		parent := path.Dir(albumScope)
		if parent != "." && parent != "/" && groupKey(path.Base(parent)) == groupKey(credit) {
			return "artist_folder", t.rootID + "/" + parent
		}
		return "album_scope", t.rootID + "/" + albumScope
	}
	return "track_scope", t.id
}

func desiredAlbum(t groupingTrack, years map[string]map[int]bool) (string, *int) {
	if t.normalizedAlbum == "" || t.creditKey == "" || t.albumScope == "" {
		return "", nil
	}
	base := t.rootID + "\x00" + t.albumScope + "\x00" + t.normalizedAlbum + "\x00" + t.creditKey
	year := t.year
	if year == nil && len(years[base]) == 1 {
		for y := range years[base] {
			year = &y
		}
	}
	if year == nil && len(years[base]) > 1 {
		return "", nil
	}
	value := 0
	if year != nil {
		value = *year
	}
	return base + fmt.Sprintf("\x00%d", value), year
}

func ensureArtist(ctx context.Context, tx pgx.Tx, t groupingTrack, credit, role, albumScope string, byID map[string]groupingTrack) (string, error) {
	code, scope := artistScope(t, albumScope, credit)
	// An album artist and track artist with identical full credit can share a
	// grouping within the same evidence scope; the membership role stays distinct.
	_ = role
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM catalog_artists WHERE normalized_credit=$1 AND evidence_scope=$2 AND rule_version=$3`, groupKey(credit), scope, groupingRuleVersion).Scan(&id)
	if err == nil {
		return id, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	// If every member of the old local grouping now has this same evidence,
	// transfer its opaque ID to the new scope instead of allocating a new one.
	var oldID string
	err = tx.QueryRow(ctx, "SELECT artist_id FROM track_artist_memberships WHERE track_id=$1 AND role=$2", t.id, role).Scan(&oldID)
	if err == nil {
		members, e := tx.Query(ctx, "SELECT track_id,role FROM track_artist_memberships WHERE artist_id=$1", oldID)
		if e != nil {
			return "", e
		}
		unanimous := true
		for members.Next() {
			var memberID, memberRole string
			if e = members.Scan(&memberID, &memberRole); e != nil {
				members.Close()
				return "", e
			}
			other, ok := byID[memberID]
			if !ok {
				unanimous = false
				continue
			}
			otherCredit := other.artist
			if memberRole == "album_artist_credit" {
				otherCredit = other.albumArtist
			}
			_, otherScope := artistScope(other, other.albumScope, nonempty(otherCredit))
			if groupKey(nonempty(otherCredit)) != groupKey(credit) || otherScope != scope {
				unanimous = false
			}
		}
		e = members.Err()
		members.Close()
		if e != nil {
			return "", e
		}
		if unanimous {
			_, e = tx.Exec(ctx, "UPDATE catalog_artists SET display_credit=$2,normalized_credit=$3,evidence_scope=$4,evidence_code=$5,source_location_id=$6,rule_version=$7 WHERE id=$1", oldID, credit, groupKey(credit), scope, code, t.sourceID, groupingRuleVersion)
			if e != nil {
				return "", e
			}
			return oldID, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	id, err = newLogicalID("art_")
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO catalog_artists(id,display_credit,normalized_credit,evidence_scope,evidence_code,source_location_id,rule_version) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, credit, groupKey(credit), scope, code, t.sourceID, groupingRuleVersion)
	return id, err
}

func ensureAlbum(ctx context.Context, tx pgx.Tx, t groupingTrack, year *int, artistID *string, byID map[string]groupingTrack, years map[string]map[int]bool) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM catalog_albums WHERE root_id=$1 AND evidence_scope=$2 AND normalized_title=$3 AND credit_key=$4 AND release_year IS NOT DISTINCT FROM $5::integer AND rule_version=$6`, t.rootID, t.albumScope, t.normalizedAlbum, t.creditKey, year, groupingRuleVersion).Scan(&id)
	if err == nil {
		_, err = tx.Exec(ctx, "UPDATE catalog_albums SET album_artist_id=$2 WHERE id=$1 AND album_artist_id IS DISTINCT FROM $2", id, artistID)
		return id, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var oldID string
	err = tx.QueryRow(ctx, "SELECT album_id FROM track_album_memberships WHERE track_id=$1", t.id).Scan(&oldID)
	if err == nil {
		members, e := tx.Query(ctx, "SELECT track_id FROM track_album_memberships WHERE album_id=$1", oldID)
		if e != nil {
			return "", e
		}
		want, _ := desiredAlbum(t, years)
		unanimous := true
		for members.Next() {
			var memberID string
			if e = members.Scan(&memberID); e != nil {
				members.Close()
				return "", e
			}
			other, ok := byID[memberID]
			if !ok {
				unanimous = false
				continue
			}
			got, _ := desiredAlbum(other, years)
			if got != want {
				unanimous = false
			}
		}
		e = members.Err()
		members.Close()
		if e != nil {
			return "", e
		}
		if unanimous {
			_, e = tx.Exec(ctx, `UPDATE catalog_albums SET display_title=$2,normalized_title=$3,album_artist_id=$4,credit_key=$5,root_id=$6,evidence_scope=$7,release_year=$8,source_location_id=$9,evidence_code=$10,rule_version=$11 WHERE id=$1`, oldID, nonempty(t.album), t.normalizedAlbum, artistID, t.creditKey, t.rootID, t.albumScope, year, t.sourceID, t.albumEvidence, groupingRuleVersion)
			if e != nil {
				return "", e
			}
			return oldID, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	id, err = newLogicalID("alb_")
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO catalog_albums(id,display_title,normalized_title,album_artist_id,credit_key,root_id,evidence_scope,release_year,source_location_id,evidence_code,rule_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, nonempty(t.album), t.normalizedAlbum, artistID, t.creditKey, t.rootID, t.albumScope, year, t.sourceID, t.albumEvidence, groupingRuleVersion)
	return id, err
}
