// Command breakglass provisions or rotates the password on a local
// (non-Azure-AD) account in app.users, for use as a break-glass login when
// Entra ID is unreachable — see the frontend's "breakglass" reveal trigger
// on the login page (src/components/auth/login-dialog.tsx in ea-ftnd-2),
// which posts to the already-live POST /api/v1/auth/login route. That
// route existed before this command did; nothing previously provisioned an
// account with a password_hash for it to authenticate against.
//
// This does NOT touch anything Azure-AD-related: LoginAzureAD and the
// /api/v1/auth/azure route are untouched, and this command only ever
// writes a row identified by --email with provider "local" — it has no
// interaction with MSAL, the Azure app registration, or JWT issuance for
// SSO users.
//
// Usage:
//
//	go run ./cmd/breakglass -email=ops@ecggh.com -name="Break Glass Admin"
//
// The password is never accepted as a flag or env var — it is always
// typed at a masked prompt, twice, so it never lands in shell history,
// process listings (ps aux), or shell/CI logs. Re-run this command anytime
// to rotate the password on the same account (matched by --email).
//
// Whether this account can reach admin-gated routes (e.g. /meters/admin)
// depends on the separate NOTIFY_EMAILS config value checked by those
// handlers — this command does not modify NOTIFY_EMAILS. Add --email's
// value there yourself if this account needs admin access, not just the
// ability to authenticate.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"bknd-3/internal/config"
	"bknd-3/internal/database"
	model "bknd-3/internal/models"
	"bknd-3/internal/services"

	"golang.org/x/term"
)

const minPasswordLen = 16

func main() {
	email := flag.String("email", "", "email of the break-glass account (required)")
	name := flag.String("name", "Break Glass Admin", "display name for the account")
	roles := flag.String("roles", "", "comma-separated roles to set on the account (optional — see NOTIFY_EMAILS note above for admin-route access)")
	flag.Parse()

	*email = strings.TrimSpace(strings.ToLower(*email))
	if *email == "" {
		fmt.Fprintln(os.Stderr, "error: -email is required")
		flag.Usage()
		os.Exit(1)
	}

	password, err := promptPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	hash, err := services.HashPassword(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error hashing password:", err)
		os.Exit(1)
	}

	cfg := config.Load()
	db, err := database.New(cfg.DatabaseURL, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error connecting to database:", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var roleList []string
	if strings.TrimSpace(*roles) != "" {
		for _, r := range strings.Split(*roles, ",") {
			if r = strings.TrimSpace(r); r != "" {
				roleList = append(roleList, r)
			}
		}
	}

	var existing model.User
	err = db.NewSelect().Model(&existing).Where("email = ?", *email).Scan(ctx)
	switch {
	case err == nil:
		// Existing account (local or otherwise) — rotate its password and
		// mark it local. Deliberately does not touch TokenVersion: bumping
		// it would invalidate every outstanding refresh token for this
		// email, which for an Azure-AD-provisioned user sharing this email
		// would sign them out unnecessarily. A plain password rotation on
		// a break-glass account doesn't need that.
		existing.PasswordHash = hash
		existing.Provider = "local"
		if *name != "" {
			existing.Name = *name
		}
		if roleList != nil {
			existing.Roles = roleList
		}
		if _, err := db.NewUpdate().Model(&existing).WherePK().Exec(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error updating account:", err)
			os.Exit(1)
		}
		fmt.Printf("Password rotated for existing account %s (id %s).\n", *email, existing.ID)
	case errors.Is(err, sql.ErrNoRows):
		u := &model.User{
			Email:        *email,
			PasswordHash: hash,
			Provider:     "local",
			Name:         *name,
			Roles:        roleList,
		}
		if _, err := db.NewInsert().Model(u).Exec(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error creating account:", err)
			os.Exit(1)
		}
		fmt.Printf("Break-glass account created: %s\n", *email)
	default:
		fmt.Fprintln(os.Stderr, "error looking up account:", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Store this password in a vault now — it is not saved or displayed again.")
	fmt.Println("This account authenticates via POST /api/v1/auth/login, reached from the")
	fmt.Println(`login page by typing "breakglass" anywhere on it.`)
	fmt.Printf("For admin-route access, add %s to NOTIFY_EMAILS separately.\n", *email)
}

func promptPassword() (string, error) {
	fmt.Fprint(os.Stderr, "New break-glass password (min ", minPasswordLen, " chars, input hidden): ")
	pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	if len(pw1) < minPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}

	fmt.Fprint(os.Stderr, "Confirm password: ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password confirmation: %w", err)
	}

	if !bytes.Equal(pw1, pw2) {
		return "", fmt.Errorf("passwords did not match")
	}
	return string(pw1), nil
}
