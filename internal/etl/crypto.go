package etl

import (
	"context"
	"errors"
	"fmt"

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

func encryptPassword(ctx context.Context, db *bun.DB, key, plaintext string) ([]byte, error) {
	if key == "" {
		return nil, errors.New("etl: ETL_CREDENTIALS_ENCRYPTION_KEY is not configured on the server")
	}
	var encrypted []byte
	if err := db.NewRaw("SELECT pgp_sym_encrypt(?, ?)", plaintext, key).Scan(ctx, &encrypted); err != nil {
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
	var password string
	if err := db.NewRaw("SELECT pgp_sym_decrypt(?, ?)", encrypted, key).Scan(ctx, &password); err != nil {
		return "", fmt.Errorf("etl: decrypt source password: %w", err)
	}
	return password, nil
}
