package storage

import "context"

// LookupFavorites resolves only the requested public IDs, without walking the
// saved collection. This is a read, not a mutation or cached source of truth.
func (s *Store) LookupFavorites(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) > 200 {
		return nil, ErrUserInvalid
	}
	for _, id := range ids {
		if !ValidCatalogID(id, "track") {
			return nil, ErrUserInvalid
		}
	}
	out := []string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT f.track_id FROM favorite_tracks f WHERE f.track_id=ANY($1::text[]) ORDER BY f.track_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
