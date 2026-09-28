// Command m17manifest fingerprints every public-schema table, its structure,
// and every public sequence without emitting row values or filesystem paths.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type tableManifest struct {
	Name                   string `json:"name"`
	Rows                   int64  `json:"rows"`
	DataSHA256             string `json:"data_sha256"`
	SchemaSHA256           string `json:"schema_sha256"`
	ColumnSchemaSHA256     string `json:"column_schema_sha256"`
	ConstraintSchemaSHA256 string `json:"constraint_schema_sha256"`
	IndexSchemaSHA256      string `json:"index_schema_sha256"`
}

type sequenceManifest struct {
	Name      string `json:"name"`
	Start     int64  `json:"start"`
	Minimum   int64  `json:"minimum"`
	Maximum   int64  `json:"maximum"`
	Increment int64  `json:"increment"`
	Cycle     bool   `json:"cycle"`
	Cache     int64  `json:"cache"`
	LastValue int64  `json:"last_value"`
	IsCalled  bool   `json:"is_called"`
}

type manifest struct {
	FormatVersion int                `json:"format_version"`
	Database      string             `json:"database"`
	SchemaSHA256  string             `json:"schema_sha256"`
	Tables        []tableManifest    `json:"tables"`
	Sequences     []sequenceManifest `json:"sequences"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "database manifest failed")
		os.Exit(1)
	}
}

func run() error {
	dsn := os.Getenv("RESONANCE_DATABASE_URL")
	if dsn == "" {
		return errors.New("database configuration is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "SET TIME ZONE 'UTC'"); err != nil {
		return err
	}
	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return err
	}
	tables, schemaParts, err := tableManifests(ctx, conn)
	if err != nil {
		return err
	}
	sequences, err := sequenceManifests(ctx, conn)
	if err != nil {
		return err
	}
	sort.Strings(schemaParts)
	schemaDigest := sha256.Sum256([]byte(strings.Join(schemaParts, "\n")))
	out := manifest{FormatVersion: 1, Database: database, SchemaSHA256: hex.EncodeToString(schemaDigest[:]), Tables: tables, Sequences: sequences}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(true)
	return encoder.Encode(out)
}

func tableManifests(ctx context.Context, conn *pgx.Conn) ([]tableManifest, []string, error) {
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name`)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	result := make([]tableManifest, 0, len(names))
	var schemas []string
	for _, name := range names {
		columns, err := columnSchema(ctx, conn, name)
		if err != nil {
			return nil, nil, err
		}
		constraints, err := constraintSchema(ctx, conn, name)
		if err != nil {
			return nil, nil, err
		}
		indexes, err := indexSchema(ctx, conn, name)
		if err != nil {
			return nil, nil, err
		}
		pk, err := primaryKeyColumns(ctx, conn, name)
		if err != nil {
			return nil, nil, err
		}
		parts := []string{"table=" + name}
		parts = append(parts, prefixRows("column", columns)...)
		parts = append(parts, prefixRows("constraint", constraints)...)
		parts = append(parts, prefixRows("index", indexes)...)
		localSchema := sha256.Sum256([]byte(strings.Join(parts, "\n")))
		dataCount, dataHash, err := tableDataHash(ctx, conn, name, pk)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, tableManifest{
			Name: name, Rows: dataCount, DataSHA256: dataHash, SchemaSHA256: hex.EncodeToString(localSchema[:]),
			ColumnSchemaSHA256: hashStrings(columns), ConstraintSchemaSHA256: hashStrings(constraints), IndexSchemaSHA256: hashStrings(indexes),
		})
		schemas = append(schemas, parts...)
	}
	return result, schemas, nil
}

func hashStrings(values []string) string {
	h := sha256.New()
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func prefixRows(prefix string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, prefix+"="+value)
	}
	return out
}

func columnSchema(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	return queryStrings(ctx, conn, `SELECT jsonb_build_object(
		'column_name',column_name,'data_type',data_type,
		'udt_name',udt_name,'is_nullable',is_nullable,'column_default',column_default,
		'character_maximum_length',character_maximum_length,'numeric_precision',numeric_precision,
		'numeric_scale',numeric_scale,'datetime_precision',datetime_precision,'collation_name',collation_name,
		'is_identity',is_identity,'identity_generation',identity_generation,'is_generated',is_generated,
		'generation_expression',generation_expression)::text
		FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
}

func constraintSchema(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	return queryStrings(ctx, conn, `SELECT jsonb_build_object('name',con.conname,'type',con.contype,
		'deferrable',con.condeferrable,'deferred',con.condeferred,'validated',con.convalidated,
		'definition',pg_get_constraintdef(con.oid,true))::text
		FROM pg_constraint con JOIN pg_class rel ON rel.oid=con.conrelid
		JOIN pg_namespace ns ON ns.oid=rel.relnamespace
		WHERE ns.nspname='public' AND rel.relname=$1 ORDER BY con.conname`, table)
}

func indexSchema(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	return queryStrings(ctx, conn, `SELECT jsonb_build_object('name',indexname,'definition',indexdef)::text
		FROM pg_indexes WHERE schemaname='public' AND tablename=$1 ORDER BY indexname`, table)
}

func queryStrings(ctx context.Context, conn *pgx.Conn, sql string, args ...any) ([]string, error) {
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func primaryKeyColumns(ctx context.Context, conn *pgx.Conn, table string) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT kcu.column_name FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu ON kcu.constraint_catalog=tc.constraint_catalog
		AND kcu.constraint_schema=tc.constraint_schema AND kcu.constraint_name=tc.constraint_name
		WHERE tc.table_schema='public' AND tc.table_name=$1 AND tc.constraint_type='PRIMARY KEY'
		ORDER BY kcu.ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func tableDataHash(ctx context.Context, conn *pgx.Conn, table string, primaryKey []string) (int64, string, error) {
	order := make([]string, 0, len(primaryKey))
	for _, name := range primaryKey {
		order = append(order, pgx.Identifier{name}.Sanitize())
	}
	if len(order) == 0 {
		order = append(order, "to_jsonb(t)::text")
	}
	query := "SELECT to_jsonb(t)::text FROM public." + pgx.Identifier{table}.Sanitize() + " AS t ORDER BY " + strings.Join(order, ",")
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	h := sha256.New()
	var count int64
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			return 0, "", err
		}
		if _, err := h.Write([]byte(row)); err != nil {
			return 0, "", err
		}
		if _, err := h.Write([]byte{'\n'}); err != nil {
			return 0, "", err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return count, hex.EncodeToString(h.Sum(nil)), nil
}

func sequenceManifests(ctx context.Context, conn *pgx.Conn) ([]sequenceManifest, error) {
	rows, err := conn.Query(ctx, `SELECT schemaname,sequencename,start_value,min_value,max_value,
		increment_by,cycle,cache_size FROM pg_sequences WHERE schemaname='public' ORDER BY sequencename`)
	if err != nil {
		return nil, err
	}
	type options struct {
		schema, name                              string
		start, minimum, maximum, increment, cache int64
		cycle                                     bool
	}
	var all []options
	for rows.Next() {
		var item options
		if err := rows.Scan(&item.schema, &item.name, &item.start, &item.minimum, &item.maximum, &item.increment, &item.cycle, &item.cache); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := make([]sequenceManifest, 0, len(all))
	for _, item := range all {
		identifier := pgx.Identifier{item.schema, item.name}.Sanitize()
		var last int64
		var called bool
		if err := conn.QueryRow(ctx, "SELECT last_value,is_called FROM "+identifier).Scan(&last, &called); err != nil {
			return nil, err
		}
		result = append(result, sequenceManifest{Name: item.schema + "." + item.name, Start: item.start, Minimum: item.minimum, Maximum: item.maximum, Increment: item.increment, Cycle: item.cycle, Cache: item.cache, LastValue: last, IsCalled: called})
	}
	return result, nil
}
