package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrUserInvalid = errors.New("invalid_request")
var ErrUserNotFound = errors.New("not_found")
var ErrStaleVersion = errors.New("stale_version")
var ErrStaleSelection = errors.New("stale_selection")
var ErrIdempotencyConflict = errors.New("idempotency_conflict")
var ErrUserLimit = errors.New("limit_exceeded")

type MutationResult struct {
	Status   int
	Body     json.RawMessage
	Replayed bool
}

// PruneExpiredReceipts performs bounded cleanup without a general job system.
// The server runs it at startup and hourly; product mutations also prune a
// small batch before receipt lookup.
func (s *Store) PruneExpiredReceipts(ctx context.Context, maxBatches int) (int64, error) {
	if maxBatches < 1 || maxBatches > 100 {
		return 0, ErrUserInvalid
	}
	var total int64
	for batch := 0; batch < maxBatches; batch++ {
		tag, err := s.pool.Exec(ctx, `DELETE FROM mutation_receipts WHERE (operation_scope,idempotency_key) IN (SELECT operation_scope,idempotency_key FROM mutation_receipts WHERE expires_at<=now() ORDER BY expires_at LIMIT 1000)`)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 1000 {
			break
		}
	}
	return total, nil
}

func ValidUUIDString(id string) bool { return validUUID(id) }

func validLogicalID(id, prefix string) bool {
	if len(id) != len(prefix)+32 || id[:len(prefix)] != prefix {
		return false
	}
	for _, c := range id[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func publicTrackExists(ctx context.Context, tx pgx.Tx, trackID string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tracks t JOIN media_objects mo ON mo.track_id=t.id JOIN media_locations ml ON ml.media_object_id=mo.id WHERE t.id=$1 AND ml.root_id IS NOT NULL)`, trackID).Scan(&exists)
	return exists, err
}

func canonicalBytes(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 262144 {
		return nil, ErrUserInvalid
	}
	return body, nil
}

func receiptLock(scope, key string) int64 {
	h := sha256.Sum256([]byte(scope + "\x00" + strings.ToLower(key)))
	return int64(binary.BigEndian.Uint64(h[:8]))
}

// mutateWithReceipt serializes retries by key. Receipt lookup precedes every
// version check in fn; the mutation, compact response, and receipt commit once.
func (s *Store) mutateWithReceipt(ctx context.Context, scope, key string, request any, fn func(pgx.Tx) (int, any, error)) (MutationResult, error) {
	if len(scope) == 0 || len(scope) > 80 || !validUUID(key) {
		return MutationResult{}, ErrUserInvalid
	}
	key = strings.ToLower(key)
	canonical, err := canonicalBytes(request)
	if err != nil {
		return MutationResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", receiptLock(scope, key)); err != nil {
		return MutationResult{}, err
	}
	// A bounded batch keeps retention work finite. The incoming expired key is
	// removed separately so an old receipt cannot block a legitimate new write.
	if _, err = tx.Exec(ctx, `DELETE FROM mutation_receipts WHERE (operation_scope,idempotency_key) IN (SELECT operation_scope,idempotency_key FROM mutation_receipts WHERE expires_at<=now() ORDER BY expires_at LIMIT 100)`); err != nil {
		return MutationResult{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM mutation_receipts WHERE operation_scope=$1 AND idempotency_key=$2 AND expires_at<=now()`, scope, key); err != nil {
		return MutationResult{}, err
	}
	var priorRequest, priorBody []byte
	var priorStatus int
	err = tx.QueryRow(ctx, `SELECT canonical_request,response_status,response_body FROM mutation_receipts WHERE operation_scope=$1 AND idempotency_key=$2`, scope, key).Scan(&priorRequest, &priorStatus, &priorBody)
	if err == nil {
		if !bytes.Equal(priorRequest, canonical) {
			return MutationResult{}, ErrIdempotencyConflict
		}
		if _, err = tx.Exec(ctx, "UPDATE mutation_receipts SET last_replayed_at=now() WHERE operation_scope=$1 AND idempotency_key=$2", scope, key); err != nil {
			return MutationResult{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return MutationResult{}, err
		}
		return MutationResult{Status: priorStatus, Body: json.RawMessage(priorBody), Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, err
	}
	status, value, err := fn(tx)
	if err != nil {
		return MutationResult{}, err
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > 16384 {
		return MutationResult{}, fmt.Errorf("%w: response too large", ErrUserLimit)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mutation_receipts(operation_scope,idempotency_key,canonical_request,response_status,response_body) VALUES($1,$2,$3,$4,$5)`, scope, key, canonical, status, body); err != nil {
		return MutationResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Status: status, Body: body}, nil
}
