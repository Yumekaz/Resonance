package storage

import "context"

// Compatibility checks for this binary's four tables, not a general schema
// drift engine. A copied migration ledger alone is not proof of compatibility.
func validateSchemaContract(ctx context.Context, q queryer) error {
	columns := []string{
		"schema_migrations|version|integer|true|", "schema_migrations|name|text|true|", "schema_migrations|checksum|bytea|true|", "schema_migrations|applied_at|timestamp with time zone|true|now()",
		"tracks|id|text|true|", "tracks|title|text|false|", "tracks|created_at|timestamp with time zone|true|now()",
		"media_objects|id|text|true|", "media_objects|track_id|text|true|", "media_objects|sha256|bytea|true|", "media_objects|format|text|true|", "media_objects|byte_length|bigint|true|", "media_objects|created_at|timestamp with time zone|true|now()",
		"media_locations|id|text|true|", "media_locations|media_object_id|text|true|", "media_locations|local_path|text|true|", "media_locations|created_at|timestamp with time zone|true|now()",
	}
	if err := matchContract(ctx, q, `SELECT c.relname || '|' || a.attname || '|' || format_type(a.atttypid,a.atttypmod) || '|' || a.attnotnull::text || '|' || coalesce(pg_get_expr(d.adbin,d.adrelid),'')
		FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
		LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
		WHERE c.oid IN (to_regclass('schema_migrations'),to_regclass('tracks'),to_regclass('media_objects'),to_regclass('media_locations'))
		AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped`, columns); err != nil {
		return err
	}
	constraints := []string{
		"schema_migrations|schema_migrations_pkey|PRIMARY KEY (version)",
		"tracks|tracks_pkey|PRIMARY KEY (id)",
		"tracks|tracks_id_nonempty|CHECK (((length(id) >= 1) AND (length(id) <= 128)))",
		"media_objects|media_objects_pkey|PRIMARY KEY (id)",
		"media_objects|media_objects_track_id_fkey|FOREIGN KEY (track_id) REFERENCES tracks(id) ON DELETE RESTRICT",
		"media_objects|media_objects_sha256_key|UNIQUE (sha256)",
		"media_objects|media_objects_id_nonempty|CHECK (((length(id) >= 1) AND (length(id) <= 128)))",
		"media_objects|media_objects_hash_length|CHECK ((octet_length(sha256) = 32))",
		"media_objects|media_objects_format_nonempty|CHECK (((length(format) >= 1) AND (length(format) <= 32)))",
		"media_objects|media_objects_byte_length|CHECK ((byte_length >= 0))",
		"media_locations|media_locations_pkey|PRIMARY KEY (id)",
		"media_locations|media_locations_media_object_id_fkey|FOREIGN KEY (media_object_id) REFERENCES media_objects(id) ON DELETE RESTRICT",
		"media_locations|media_locations_id_nonempty|CHECK (((length(id) >= 1) AND (length(id) <= 128)))",
		"media_locations|media_locations_path_nonempty|CHECK ((length(local_path) > 0))",
	}
	if err := matchContract(ctx, q, `SELECT c.relname || '|' || k.conname || '|' || pg_get_constraintdef(k.oid)
		FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid LEFT JOIN pg_index i ON i.indexrelid=k.conindid
		WHERE c.oid IN (to_regclass('schema_migrations'),to_regclass('tracks'),to_regclass('media_objects'),to_regclass('media_locations'))
		AND k.convalidated AND (k.conindid=0 OR (i.indisvalid AND i.indisready))`, constraints); err != nil {
		return err
	}
	return matchContract(ctx, q, `SELECT c.relname || '|' || x.relname || '|' || pg_get_indexdef(i.indexrelid,1,true)
		FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class x ON x.oid=i.indexrelid
		JOIN pg_am am ON am.oid=x.relam
		WHERE c.oid IN (to_regclass('media_objects'),to_regclass('media_locations'))
		AND x.relname IN ('media_objects_track_id_idx','media_locations_media_object_id_idx')
		AND i.indisvalid AND i.indisready AND NOT i.indisunique AND i.indnkeyatts=1 AND i.indnatts=1
		AND i.indpred IS NULL AND i.indexprs IS NULL AND am.amname='btree'`, []string{
		"media_objects|media_objects_track_id_idx|track_id", "media_locations|media_locations_media_object_id_idx|media_object_id",
	})
}

func matchContract(ctx context.Context, q queryer, sql string, expected []string) error {
	remaining := make(map[string]bool, len(expected))
	for _, item := range expected {
		remaining[item] = true
	}
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return ErrSchemaMismatch
	}
	defer rows.Close()
	for rows.Next() {
		var item string
		if rows.Scan(&item) != nil || !remaining[item] {
			return ErrSchemaMismatch
		}
		delete(remaining, item)
	}
	if rows.Err() != nil || len(remaining) != 0 {
		return ErrSchemaMismatch
	}
	return nil
}
