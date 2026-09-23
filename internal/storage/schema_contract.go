package storage

import (
	"context"
	"fmt"
)

// Compatibility checks for this binary's catalog tables, not a general schema
// drift engine. A copied migration ledger alone is not proof of compatibility.
func validateSchemaContract(ctx context.Context, q queryer) error {
	columns := []string{
		"schema_migrations|version|integer|true|", "schema_migrations|name|text|true|", "schema_migrations|checksum|bytea|true|", "schema_migrations|applied_at|timestamp with time zone|true|now()",
		"tracks|id|text|true|", "tracks|title|text|false|", "tracks|created_at|timestamp with time zone|true|now()",
		"tracks|artist_credit|text|false|", "tracks|album_title|text|false|", "tracks|album_artist_credit|text|false|", "tracks|track_number|integer|false|", "tracks|disc_number|integer|false|", "tracks|release_year|integer|false|", "tracks|genre|text|false|", "tracks|title_source|text|false|", "tracks|metadata_source_location_id|text|false|",
		"media_objects|id|text|true|", "media_objects|track_id|text|true|", "media_objects|sha256|bytea|true|", "media_objects|format|text|true|", "media_objects|byte_length|bigint|true|", "media_objects|created_at|timestamp with time zone|true|now()", "media_objects|artwork_sha256|bytea|false|", "media_objects|artwork_mime|text|false|",
		"media_locations|id|text|true|", "media_locations|media_object_id|text|true|", "media_locations|local_path|text|true|", "media_locations|created_at|timestamp with time zone|true|now()", "media_locations|root_id|uuid|false|", "media_locations|relative_path|text|false|",
		"media_locations|availability|text|true|'available'::text", "media_locations|unavailable_reason|text|false|", "media_locations|unavailable_at|timestamp with time zone|false|", "media_locations|observed_size|bigint|false|", "media_locations|observed_mtime_ns|bigint|false|", "media_locations|native_id_kind|text|false|", "media_locations|native_id_scope|text|false|", "media_locations|native_id|bytea|false|", "media_locations|native_birth_token|bytea|false|", "media_locations|last_seen_run_id|uuid|false|", "media_locations|state_updated_at|timestamp with time zone|true|now()",
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
		AND k.conname IN ('schema_migrations_pkey','tracks_pkey','tracks_id_nonempty','media_objects_pkey','media_objects_track_id_fkey','media_objects_sha256_key','media_objects_id_nonempty','media_objects_hash_length','media_objects_format_nonempty','media_objects_byte_length','media_locations_pkey','media_locations_media_object_id_fkey','media_locations_id_nonempty','media_locations_path_nonempty')
		AND k.convalidated AND (k.conindid=0 OR (i.indisvalid AND i.indisready))`, constraints); err != nil {
		return err
	}
	if err := matchContract(ctx, q, `SELECT c.relname || '|' || x.relname || '|' || pg_get_indexdef(i.indexrelid,1,true)
		FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class x ON x.oid=i.indexrelid
		JOIN pg_am am ON am.oid=x.relam
		WHERE c.oid IN (to_regclass('media_objects'),to_regclass('media_locations'))
		AND x.relname IN ('media_objects_track_id_idx','media_locations_media_object_id_idx')
		AND i.indisvalid AND i.indisready AND NOT i.indisunique AND i.indnkeyatts=1 AND i.indnatts=1
		AND i.indpred IS NULL AND i.indexprs IS NULL AND am.amname='btree'`, []string{
		"media_objects|media_objects_track_id_idx|track_id", "media_locations|media_locations_media_object_id_idx|media_object_id",
	}); err != nil {
		return err
	}
	return validateLibraryContract(ctx, q)
}

func validateLibraryContract(ctx context.Context, q queryer) error {
	columns := []string{
		"library_roots|id|uuid|true|", "library_roots|name|text|true|", "library_roots|canonical_path|text|true|", "library_roots|path_key|text|true|", "library_roots|enabled|boolean|true|true", "library_roots|created_at|timestamp with time zone|true|now()", "library_roots|last_successful_scan_id|uuid|false|",
		"scan_runs|id|uuid|true|", "scan_runs|root_id|uuid|true|", "scan_runs|status|text|true|", "scan_runs|files_visited|bigint|true|0", "scan_runs|files_supported|bigint|true|0", "scan_runs|imported|bigint|true|0", "scan_runs|skipped|bigint|true|0", "scan_runs|failed|bigint|true|0", "scan_runs|bytes_hashed|bigint|true|0", "scan_runs|metadata_extractions|bigint|true|0", "scan_runs|error_code|text|false|", "scan_runs|started_at|timestamp with time zone|true|now()", "scan_runs|finished_at|timestamp with time zone|false|",
		"scan_runs|phase|text|true|'discovering'::text", "scan_runs|traversal_complete|boolean|true|false", "scan_runs|observations_applied|boolean|true|false", "scan_runs|absence_reconciled|boolean|true|false", "scan_runs|files_unchanged|bigint|true|0", "scan_runs|files_hashed|bigint|true|0", "scan_runs|stat_changed_same_bytes|bigint|true|0", "scan_runs|changed_bytes|bigint|true|0", "scan_runs|locations_added|bigint|true|0", "scan_runs|locations_moved|bigint|true|0", "scan_runs|locations_unavailable|bigint|true|0", "scan_runs|media_objects_created|bigint|true|0", "scan_runs|tracks_created|bigint|true|0",
		"scan_errors|id|bigint|true|", "scan_errors|run_id|uuid|true|", "scan_errors|relative_path|text|true|", "scan_errors|code|text|true|", "scan_errors|created_at|timestamp with time zone|true|now()",
	}
	if err := matchContract(ctx, q, `SELECT c.relname || '|' || a.attname || '|' || format_type(a.atttypid,a.atttypmod) || '|' || a.attnotnull::text || '|' || coalesce(pg_get_expr(d.adbin,d.adrelid),'')
		FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
		LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
		WHERE c.oid IN (to_regclass('library_roots'),to_regclass('scan_runs'),to_regclass('scan_errors'))
		AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped`, columns); err != nil {
		return err
	}
	constraints := []string{
		"library_roots|library_roots_pkey|PRIMARY KEY (id)",
		"library_roots|library_roots_name_check|CHECK (((length(name) >= 1) AND (length(name) <= 128)))",
		"library_roots|library_roots_canonical_path_check|CHECK ((length(canonical_path) > 0))",
		"library_roots|library_roots_path_key_key|UNIQUE (path_key)",
		"tracks|tracks_track_number_check|CHECK ((track_number > 0))",
		"tracks|tracks_disc_number_check|CHECK ((disc_number > 0))",
		"tracks|tracks_release_year_check|CHECK (((release_year >= 1) AND (release_year <= 9999)))",
		"tracks|tracks_title_source_check|CHECK ((title_source = ANY (ARRAY['tag'::text, 'filename'::text])))",
		"media_objects|media_objects_artwork_sha256_check|CHECK (((artwork_sha256 IS NULL) OR (octet_length(artwork_sha256) = 32)))",
		"media_locations|media_locations_root_id_fkey|FOREIGN KEY (root_id) REFERENCES library_roots(id)",
		"media_locations|media_locations_root_pair|CHECK (((root_id IS NULL) = (relative_path IS NULL)))",
		"media_locations|media_locations_availability_check|CHECK ((availability = ANY (ARRAY['available'::text, 'unavailable'::text])))",
		"media_locations|media_locations_availability_state_check|CHECK ((((availability = 'available'::text) AND (unavailable_reason IS NULL) AND (unavailable_at IS NULL)) OR ((availability = 'unavailable'::text) AND (unavailable_reason IS NOT NULL) AND (unavailable_at IS NOT NULL))))",
		"media_locations|media_locations_observed_size_check|CHECK (((observed_size IS NULL) OR (observed_size >= 0)))",
		"media_locations|media_locations_native_identity_check|CHECK ((((native_id_kind IS NULL) AND (native_id_scope IS NULL) AND (native_id IS NULL)) OR ((native_id_kind IS NOT NULL) AND (native_id_scope IS NOT NULL) AND (native_id IS NOT NULL))))",
		"media_locations|media_locations_last_seen_run_fkey|FOREIGN KEY (root_id, last_seen_run_id) REFERENCES scan_runs(root_id, id)",
		"tracks|tracks_metadata_source_location_fkey|FOREIGN KEY (metadata_source_location_id) REFERENCES media_locations(id) DEFERRABLE INITIALLY DEFERRED",
		"scan_runs|scan_runs_pkey|PRIMARY KEY (id)",
		"scan_runs|scan_runs_root_id_fkey|FOREIGN KEY (root_id) REFERENCES library_roots(id)",
		"scan_runs|scan_runs_status_check|CHECK ((status = ANY (ARRAY['running'::text, 'succeeded'::text, 'partial'::text, 'failed'::text, 'canceled'::text])))",
		"scan_runs|scan_runs_phase_check|CHECK ((phase = ANY (ARRAY['discovering'::text, 'publishing'::text, 'finished'::text])))",
		"scan_runs|scan_runs_counters_nonnegative|CHECK (((files_visited >= 0) AND (files_supported >= 0) AND (imported >= 0) AND (skipped >= 0) AND (failed >= 0) AND (bytes_hashed >= 0) AND (metadata_extractions >= 0) AND (files_unchanged >= 0) AND (files_hashed >= 0) AND (stat_changed_same_bytes >= 0) AND (changed_bytes >= 0) AND (locations_added >= 0) AND (locations_moved >= 0) AND (locations_unavailable >= 0) AND (media_objects_created >= 0) AND (tracks_created >= 0)))",
		"scan_runs|scan_runs_root_id_id_key|UNIQUE (root_id, id)",
		"library_roots|library_roots_last_successful_scan_fkey|FOREIGN KEY (id, last_successful_scan_id) REFERENCES scan_runs(root_id, id)",
		"scan_errors|scan_errors_pkey|PRIMARY KEY (id)",
		"scan_errors|scan_errors_run_id_fkey|FOREIGN KEY (run_id) REFERENCES scan_runs(id) ON DELETE CASCADE",
		"scan_errors|scan_errors_relative_path_check|CHECK ((length(relative_path) <= 512))",
		"scan_errors|scan_errors_code_check|CHECK (((length(code) >= 1) AND (length(code) <= 64)))",
	}
	if err := matchContract(ctx, q, `SELECT c.relname || '|' || k.conname || '|' || pg_get_constraintdef(k.oid)
		FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid LEFT JOIN pg_index i ON i.indexrelid=k.conindid
		WHERE c.oid IN (to_regclass('library_roots'),to_regclass('tracks'),to_regclass('media_objects'),to_regclass('media_locations'),to_regclass('scan_runs'),to_regclass('scan_errors'))
		AND k.conname IN ('library_roots_pkey','library_roots_name_check','library_roots_canonical_path_check','library_roots_path_key_key','library_roots_last_successful_scan_fkey','tracks_track_number_check','tracks_disc_number_check','tracks_release_year_check','tracks_title_source_check','tracks_metadata_source_location_fkey','media_objects_artwork_sha256_check','media_locations_root_id_fkey','media_locations_root_pair','media_locations_availability_check','media_locations_availability_state_check','media_locations_observed_size_check','media_locations_native_identity_check','media_locations_last_seen_run_fkey','scan_runs_pkey','scan_runs_root_id_fkey','scan_runs_status_check','scan_runs_phase_check','scan_runs_counters_nonnegative','scan_runs_root_id_id_key','scan_errors_pkey','scan_errors_run_id_fkey','scan_errors_relative_path_check','scan_errors_code_check')
		AND k.convalidated AND (k.conindid=0 OR (i.indisvalid AND i.indisready))`, constraints); err != nil {
		return err
	}
	return matchContract(ctx, q, `SELECT c.relname || '|' || x.relname || '|' || i.indisunique::text || '|' || i.indnkeyatts::text || '|' || keys.columns || '|' || coalesce(pg_get_expr(i.indpred,i.indrelid),'') || '|' || i.indoption::text
		FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class x ON x.oid=i.indexrelid JOIN pg_am am ON am.oid=x.relam
		CROSS JOIN LATERAL (
			SELECT string_agg(a.attname, ',' ORDER BY k.ordinality) AS columns
			FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, ordinality)
			JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum
			WHERE k.ordinality <= i.indnkeyatts
		) AS keys
		WHERE x.oid IN (to_regclass('media_locations_active_root_relative_idx'),to_regclass('media_locations_root_relative_idx'),to_regclass('media_locations_root_availability_idx'),to_regclass('media_locations_native_lookup_idx'),to_regclass('scan_runs_root_started_idx'),to_regclass('scan_errors_run_idx'))
		AND i.indisvalid AND i.indisready AND i.indnatts=i.indnkeyatts AND i.indexprs IS NULL AND am.amname='btree'`, []string{
		"media_locations|media_locations_active_root_relative_idx|true|2|root_id,relative_path|((root_id IS NOT NULL) AND (availability = 'available'::text))|0 0",
		"media_locations|media_locations_root_availability_idx|false|2|root_id,availability||0 0",
		"media_locations|media_locations_native_lookup_idx|false|4|root_id,native_id_kind,native_id_scope,native_id|((availability = 'available'::text) AND (native_id IS NOT NULL))|0 0 0 0",
		"scan_runs|scan_runs_root_started_idx|false|2|root_id,started_at||0 3",
		"scan_errors|scan_errors_run_idx|false|1|run_id||0",
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
			return fmt.Errorf("%w: unexpected schema item %q", ErrSchemaMismatch, item)
		}
		delete(remaining, item)
	}
	if rows.Err() != nil {
		return ErrSchemaMismatch
	}
	if len(remaining) != 0 {
		for item := range remaining {
			return fmt.Errorf("%w: missing schema item %q", ErrSchemaMismatch, item)
		}
	}
	return nil
}
