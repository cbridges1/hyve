package orgdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"
)

// User is one person's login: the username, email, and password shared by
// every organization they belong to. Their memberships are the local
// Bindings whose Identity is Username — a Binding carries only the
// namespace, environment, and role. See migrations/*/0007_users.sql.
type User struct {
	ID       string
	Username string
	// Email is unique across users when set; also accepted as a login
	// identifier (see internal/api.handleLogin).
	Email        *string
	PasswordHash *string
	CreatedAt    time.Time
}

const userColumns = `id, username, email, password_hash, created_at`

func scanUser(row *sql.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

// CreateUser inserts u. Username is required and unique; so is Email when
// set.
func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	if u.ID == "" {
		u.ID = newID()
	}
	if u.Username == "" {
		return User{}, fmt.Errorf("create user: username is required")
	}
	if _, err := s.exec(ctx, `INSERT INTO users (id, username, email, password_hash) VALUES (?, ?, ?, ?)`,
		u.ID, u.Username, u.Email, u.PasswordHash); err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return s.GetUserByID(ctx, u.ID)
}

// GetUserByID, GetUserByUsername and GetUserByEmail return ErrNotFound when
// no user matches.
func (s *Store) GetUserByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.queryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(s.queryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username = ?`, username))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.queryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email))
}

// ListUsers returns every user, by username — for Migrate.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.query(ctx, `SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserPassword replaces id's password hash.
func (s *Store) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	if _, err := s.exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id); err != nil {
		return fmt.Errorf("set user password: %w", err)
	}
	return nil
}

// SetUserEmail replaces id's email; nil clears it.
func (s *Store) SetUserEmail(ctx context.Context, id string, email *string) error {
	if _, err := s.exec(ctx, `UPDATE users SET email = ? WHERE id = ?`, email, id); err != nil {
		return fmt.Errorf("set user email: %w", err)
	}
	return nil
}

// DeleteUser removes a user and their password reset token. Their bindings
// and sessions are the caller's to remove first (see internal/api's
// account deletion, which only deletes a user once no membership is left).
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	if err := s.DeletePasswordResetTokensForUser(ctx, id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// ListBindingsForIdentity returns every (subjectType, identity) binding in
// every namespace — a user's memberships across organizations.
func (s *Store) ListBindingsForIdentity(ctx context.Context, subjectType, identity string) ([]Binding, error) {
	rows, err := s.query(ctx, `SELECT `+bindingColumns+` FROM bindings WHERE subject_type = ? AND identity = ? ORDER BY namespace`, subjectType, identity)
	if err != nil {
		return nil, fmt.Errorf("query bindings for identity: %w", err)
	}
	defer rows.Close()
	return scanBindings(rows)
}

// EnsureUsersFromBindings gives every local binding identity with no user
// row one, built from that identity's bindings — the one-time move from
// per-organization accounts (credentials on each binding) to one user per
// username. Bindings sharing a username become one user's memberships:
// the password comes from the most recently created binding that has one
// (the others' passwords stop working), and the email from the most
// recent binding that has one not already taken by another user. Safe to
// run on every startup: identities that already have a user are skipped.
// Returns how many users it created.
func (s *Store) EnsureUsersFromBindings(ctx context.Context) (int, error) {
	bindings, err := s.ListAllBindings(ctx)
	if err != nil {
		return 0, err
	}
	byIdentity := map[string][]Binding{}
	for _, b := range bindings {
		if b.SubjectType == SubjectTypeLocal {
			byIdentity[b.Identity] = append(byIdentity[b.Identity], b)
		}
	}
	identities := make([]string, 0, len(byIdentity))
	for id := range byIdentity {
		identities = append(identities, id)
	}
	sort.Strings(identities)

	created := 0
	for _, identity := range identities {
		if _, err := s.GetUserByUsername(ctx, identity); err == nil {
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return created, err
		}
		group := byIdentity[identity]
		sort.SliceStable(group, func(i, j int) bool { return group[i].CreatedAt.After(group[j].CreatedAt) })

		u := User{Username: identity}
		for _, b := range group {
			if b.PasswordHash != nil {
				u.PasswordHash = b.PasswordHash
				break
			}
		}
		for _, b := range group {
			if b.Email == nil {
				continue
			}
			if _, err := s.GetUserByEmail(ctx, *b.Email); err == nil {
				log.Printf("orgdb: user %q: email %q already belongs to another user — not carried over", identity, *b.Email)
				continue
			}
			u.Email = b.Email
			break
		}
		if _, err := s.CreateUser(ctx, u); err != nil {
			return created, fmt.Errorf("create user %q from its bindings: %w", identity, err)
		}
		if len(group) > 1 {
			log.Printf("orgdb: merged %d accounts named %q into one user (password from the most recently created)", len(group), identity)
		}
		created++
	}
	return created, nil
}

// EnsureUser returns username's user, creating it (with passwordHash and
// email) when it doesn't exist yet. For an existing user, a non-nil
// passwordHash replaces theirs; email is only used at creation. created
// reports which happened.
func (s *Store) EnsureUser(ctx context.Context, username string, passwordHash, email *string) (u User, created bool, err error) {
	u, err = s.GetUserByUsername(ctx, username)
	if errors.Is(err, ErrNotFound) {
		u, err = s.CreateUser(ctx, User{Username: username, PasswordHash: passwordHash, Email: email})
		return u, err == nil, err
	}
	if err != nil {
		return User{}, false, err
	}
	if passwordHash != nil {
		if err := s.SetUserPassword(ctx, u.ID, *passwordHash); err != nil {
			return User{}, false, err
		}
		u.PasswordHash = passwordHash
	}
	return u, false, nil
}
