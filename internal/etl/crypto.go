package etl

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/uptrace/bun"
)

// encryptPassword and decryptPassword do the actual pgcrypto work for
// app.etl_sources.password_encrypted — see
// sql/etl_sources_password_encryption.sql for why a DB-side symmetric
// cipher was chosen over the app doing its own crypto in Go: it keeps the
// key out of the query text (bound as a parameter, same as any other
// value) and reuses a well-audited, already-enabled Postgres extension
// instead of a new Go crypto dependency.
//
// Both are plain package functions (not Service/Engine methods) so both
// can call them against whichever *bun.DB they already hold, without a
// dependency between the two types.

// pgcryptoSchemaCache remembers the schema pgp_sym_encrypt/pgp_sym_decrypt
// resolved to, once — guarded by a mutex rather than sync.Once so a
// not-yet-installed extension doesn't permanently wedge every future call
// into the same failure (e.g. an admin installs it after the server has
// already started).
var (
	pgcryptoSchemaMu    sync.Mutex
	pgcryptoSchemaCache string
)

// pgcryptoSchema returns the schema pgcrypto's functions actually live in
// and quotes it for direct interpolation into a query. A bare, unqualified
// pgp_sym_encrypt(...)/pgp_sym_decrypt(...) call only works if that schema
// happens to be on the connecting role's search_path — true when pgcrypto
// was installed with a plain `CREATE EXTENSION pgcrypto` into "public",
// false on managed Postgres providers that default extensions into a
// separate schema (Supabase uses "extensions") that isn't automatically
// on every role's search_path. Resolving and qualifying here makes both
// encrypt/decrypt work regardless of which convention the target database
// uses, without requiring a DB-side search_path change. Confirmed against
// a real Postgres 16 with pgcrypto installed into a non-search_path
// "extensions" schema (reproducing "function pgp_sym_encrypt(unknown,
// unknown) does not exist") — the unqualified call fails exactly that
// way, the schema-qualified one succeeds.
func pgcryptoSchema(ctx context.Context, db *bun.DB) (string, error) {
	pgcryptoSchemaMu.Lock()
	cached := pgcryptoSchemaCache
	pgcryptoSchemaMu.Unlock()
	if cached != "" {
		return cached, nil
	}

	var schema string
	err := db.NewRaw(`
		SELECT n.nspname FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname = 'pgcrypto'
	`).Scan(ctx, &schema)
	if err != nil {
		return "", fmt.Errorf(`etl: pgcrypto extension is not installed on this database — run "CREATE EXTENSION IF NOT EXISTS pgcrypto;" against it: %w`, err)
	}
	quoted, err := quoteIdent(schema)
	if err != nil {
		return "", fmt.Errorf("etl: pgcrypto extension schema %q: %w", schema, err)
	}

	pgcryptoSchemaMu.Lock()
	pgcryptoSchemaCache = quoted
	pgcryptoSchemaMu.Unlock()
	return quoted, nil
}

func encryptPassword(ctx context.Context, db *bun.DB, key, plaintext string) ([]byte, error) {
	if key == "" {
		return nil, errors.New("etl: ETL_CREDENTIALS_ENCRYPTION_KEY is not configured on the server")
	}
	schema, err := pgcryptoSchema(ctx, db)
	if err != nil {
		return nil, err
	}
	var encrypted []byte
	q := fmt.Sprintf("SELECT %s.pgp_sym_encrypt(?, ?)", schema)
	if err := db.NewRaw(q, plaintext, key).Scan(ctx, &encrypted); err != nil {
		return nil, fmt.Errorf("etl: encrypt source password: %w", err)
	}
	return encrypted, nil
}

func decryptPassword(ctx context.Context, db *bun.DB, key string, encrypted []byte) (string, error) {
	if len(encrypted) == 0 {
		return "", errors.New("etl: source has no password set")
	}
	if key == "" {
		return "", errors.New("etl: ETL_CREDENTIALS_ENCRYPTION_KEY is not configured on the server")
	}
	schema, err := pgcryptoSchema(ctx, db)
	if err != nil {
		return "", err
	}
	var password string
	q := fmt.Sprintf("SELECT %s.pgp_sym_decrypt(?, ?)", schema)
	if err := db.NewRaw(q, encrypted, key).Scan(ctx, &password); err != nil {
		return "", fmt.Errorf("etl: decrypt source password: %w", err)
	}
	return password, nil
}
