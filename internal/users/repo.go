package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/app/internal/platform/store"
	"example.com/app/internal/store/sqlc"
)

const emailConstraint = "users_email_lower_key"

// Repo implements Repository with sqlc over a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// NewRepo returns a Repo backed by pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool, q: sqlc.New(pool)}
}

// CreateUser inserts a user, translating the unique-email violation to ErrEmailTaken.
func (r *Repo) CreateUser(ctx context.Context, email, passwordHash string, role Role) (User, error) {
	row, err := r.q.CreateUser(ctx, sqlc.CreateUserParams{
		Email:        email,
		PasswordHash: passwordHash,
		Role:         string(role),
	})
	if err != nil {
		if constraint, ok := store.UniqueViolation(err); ok && constraint == emailConstraint {
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("users: create user: %w", err)
	}
	return toUser(row), nil
}

// GetUserByEmail finds a user ignoring case, or returns ErrNotFound.
func (r *Repo) GetUserByEmail(ctx context.Context, email string) (User, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		return User{}, notFound(err, "get user by email")
	}
	return toUser(row), nil
}

// GetUserByID finds a user, or returns ErrNotFound.
func (r *Repo) GetUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	row, err := r.q.GetUserByID(ctx, id)
	if err != nil {
		return User{}, notFound(err, "get user by id")
	}
	return toUser(row), nil
}

// UpdatePasswordHash replaces a user's stored hash.
func (r *Repo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, passwordHash string) error {
	err := r.q.UpdateUserPasswordHash(ctx, sqlc.UpdateUserPasswordHashParams{
		PasswordHash: passwordHash,
		ID:           id,
	})
	if err != nil {
		return fmt.Errorf("users: update password hash: %w", err)
	}
	return nil
}

// CountAdmins returns how many admin accounts exist.
func (r *Repo) CountAdmins(ctx context.Context) (int64, error) {
	n, err := r.q.CountAdmins(ctx)
	if err != nil {
		return 0, fmt.Errorf("users: count admins: %w", err)
	}
	return n, nil
}

// InsertRefreshToken stores a refresh token record.
func (r *Repo) InsertRefreshToken(ctx context.Context, token RefreshToken) error {
	_, err := r.q.InsertRefreshToken(ctx, sqlc.InsertRefreshTokenParams{
		UserID:    token.UserID,
		FamilyID:  token.FamilyID,
		TokenHash: token.TokenHash,
		ExpiresAt: token.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("users: insert refresh token: %w", err)
	}
	return nil
}

// RotateRefreshToken runs the rotation in one transaction: lock the presented
// token, apply RefreshToken.Verdict, then either rotate, or revoke the family
// on reuse. The revocation commits even though the call returns ErrTokenReuse.
func (r *Repo) RotateRefreshToken(ctx context.Context, presentedHash, nextHash []byte, nextExpiresAt, now time.Time) (RefreshToken, error) {
	var (
		current RefreshToken
		outcome error
	)
	err := store.InTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := r.q.WithTx(tx)

		row, err := q.GetRefreshTokenByHashForUpdate(ctx, presentedHash)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalidToken
			return nil
		}
		if err != nil {
			return err
		}
		current = toRefreshToken(row)

		switch current.Verdict(now) {
		case VerdictReuse:
			if _, err := q.RevokeRefreshTokenFamily(ctx, current.FamilyID); err != nil {
				return err
			}
			outcome = ErrTokenReuse
			return nil
		case VerdictInvalid:
			outcome = ErrInvalidToken
			return nil
		case VerdictOK:
		}

		rotated, err := q.MarkRefreshTokenRotated(ctx, current.ID)
		if err != nil {
			return err
		}
		if rotated == 0 {
			outcome = ErrInvalidToken
			return nil
		}
		_, err = q.InsertRefreshToken(ctx, sqlc.InsertRefreshTokenParams{
			UserID:    current.UserID,
			FamilyID:  current.FamilyID,
			TokenHash: nextHash,
			ExpiresAt: nextExpiresAt,
		})
		return err
	})
	if err != nil {
		return RefreshToken{}, fmt.Errorf("users: rotate refresh token: %w", err)
	}
	if outcome != nil {
		return RefreshToken{}, outcome
	}
	return current, nil
}

// RevokeRefreshFamily revokes every live token in a family.
func (r *Repo) RevokeRefreshFamily(ctx context.Context, familyID uuid.UUID) error {
	if _, err := r.q.RevokeRefreshTokenFamily(ctx, familyID); err != nil {
		return fmt.Errorf("users: revoke family: %w", err)
	}
	return nil
}

// RevokeRefreshFamilyByHash revokes the family of the token with this hash. An
// unknown hash is not an error.
func (r *Repo) RevokeRefreshFamilyByHash(ctx context.Context, tokenHash []byte) error {
	row, err := r.q.GetRefreshTokenByHashForUpdate(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("users: find refresh token: %w", err)
	}
	return r.RevokeRefreshFamily(ctx, row.FamilyID)
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("users: %s: %w", op, err)
}

func toUser(row sqlc.User) User {
	return User{
		ID:           row.ID,
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
		Role:         Role(row.Role),
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		DisabledAt:   row.DisabledAt,
	}
}

func toRefreshToken(row sqlc.RefreshToken) RefreshToken {
	return RefreshToken{
		ID:        row.ID,
		UserID:    row.UserID,
		FamilyID:  row.FamilyID,
		TokenHash: row.TokenHash,
		ExpiresAt: row.ExpiresAt,
		RotatedAt: row.RotatedAt,
		RevokedAt: row.RevokedAt,
		CreatedAt: row.CreatedAt,
	}
}
