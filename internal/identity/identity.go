package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInviteInvalid      = errors.New("invite is invalid or expired")
	ErrConflict           = errors.New("username or email already exists")
	ErrForbidden          = errors.New("forbidden")
	ErrNotFound           = errors.New("not found")
	usernamePattern       = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,24}$`)
)

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	Nickname     string `json:"nickname"`
	Bio          string `json:"bio"`
	BedNo        string `json:"bed_no"`
	MemorialNote string `json:"memorial_note"`
	AvatarPath   string `json:"avatar_path"`
}

type Member struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Nickname     string `json:"nickname"`
	AvatarPath   string `json:"avatar_path"`
	Bio          string `json:"bio"`
	BedNo        string `json:"bed_no"`
	MemorialNote string `json:"memorial_note"`
}

type AdminUser struct {
	User
	CreatedAt          time.Time `json:"created_at"`
	ActiveSessionCount int       `json:"active_session_count"`
}

type AdminUserUpdate struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

type Session struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

type RegisterInput struct {
	InviteCode string
	Username   string
	Email      string
	Password   string
	Nickname   string
}

type ProfileInput struct {
	Nickname     string `json:"nickname"`
	Bio          string `json:"bio"`
	BedNo        string `json:"bed_no"`
	MemorialNote string `json:"memorial_note"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) ListMembers(ctx context.Context) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, p.nickname, p.avatar_path, p.bio, p.bed_no, p.memorial_note
		FROM users u JOIN profiles p ON p.user_id = u.id WHERE u.status = 'active' ORDER BY p.nickname COLLATE NOCASE, u.username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.ID, &member.Username, &member.Nickname, &member.AvatarPath, &member.Bio, &member.BedNo, &member.MemorialNote); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *Store) ListAdminUsers(ctx context.Context, actor User, search, role, status string) ([]AdminUser, error) {
	if actor.Role != "admin" {
		return nil, ErrForbidden
	}
	search, role, status = strings.TrimSpace(search), strings.TrimSpace(role), strings.TrimSpace(status)
	query := `SELECT u.id, u.username, u.email, u.role, u.status, p.nickname, p.bio, p.bed_no, p.memorial_note, p.avatar_path,
		u.created_at, (SELECT COUNT(*) FROM sessions s WHERE s.user_id = u.id AND s.revoked_at IS NULL AND s.expires_at > ?)
		FROM users u JOIN profiles p ON p.user_id = u.id WHERE 1 = 1`
	args := []any{nowText()}
	if search != "" {
		query += ` AND (u.username LIKE ? COLLATE NOCASE OR u.email LIKE ? COLLATE NOCASE OR p.nickname LIKE ? COLLATE NOCASE)`
		needle := "%" + search + "%"
		args = append(args, needle, needle, needle)
	}
	if role == "admin" || role == "member" {
		query += ` AND u.role = ?`
		args = append(args, role)
	}
	if status == "active" || status == "disabled" {
		query += ` AND u.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY CASE u.role WHEN 'admin' THEN 0 ELSE 1 END, p.nickname COLLATE NOCASE, u.username COLLATE NOCASE`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminUser{}
	for rows.Next() {
		var item AdminUser
		var created string
		if err := rows.Scan(&item.ID, &item.Username, &item.Email, &item.Role, &item.Status, &item.Nickname, &item.Bio, &item.BedNo, &item.MemorialNote, &item.AvatarPath, &created, &item.ActiveSessionCount); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateAdminUser(ctx context.Context, actor User, targetID string, input AdminUserUpdate, ip string) (AdminUser, error) {
	if actor.Role != "admin" {
		return AdminUser{}, ErrForbidden
	}
	input.Role, input.Status = strings.TrimSpace(input.Role), strings.TrimSpace(input.Status)
	if (input.Role != "admin" && input.Role != "member") || (input.Status != "active" && input.Status != "disabled") {
		return AdminUser{}, errors.New("invalid user role or status")
	}
	if targetID == actor.ID && (input.Role != actor.Role || input.Status != actor.Status) {
		return AdminUser{}, errors.New("不能修改当前登录管理员的角色或状态")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, err
	}
	defer tx.Rollback()
	var currentRole, currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT role, status FROM users WHERE id = ?`, targetID).Scan(&currentRole, &currentStatus); errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	} else if err != nil {
		return AdminUser{}, err
	}
	if currentRole == "admin" && currentStatus == "active" && (input.Role != "admin" || input.Status != "active") {
		var admins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND status = 'active'`).Scan(&admins); err != nil {
			return AdminUser{}, err
		}
		if admins <= 1 {
			return AdminUser{}, errors.New("必须保留至少一个启用中的管理员")
		}
	}
	now := nowText()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, status = ?, updated_at = ? WHERE id = ?`, input.Role, input.Status, now, targetID); err != nil {
		return AdminUser{}, err
	}
	if input.Status == "disabled" {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE user_id = ?`, now, targetID); err != nil {
			return AdminUser{}, err
		}
	}
	metadata := fmt.Sprintf(`{"role":"%s","status":"%s"}`, input.Role, input.Status)
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, ip_address, created_at) VALUES(?, 'admin.user.update', 'user', ?, ?, ?, ?)`, actor.ID, targetID, metadata, ip, now); err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, err
	}
	items, err := s.ListAdminUsers(ctx, actor, "", "", "")
	if err != nil {
		return AdminUser{}, err
	}
	for _, item := range items {
		if item.ID == targetID {
			return item, nil
		}
	}
	return AdminUser{}, ErrNotFound
}

func (s *Store) BootstrapAdmin(ctx context.Context, username, email, password, nickname string) (bool, error) {
	if username == "" {
		return false, nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if err := validateAccount(username, email, password, nickname); err != nil {
		return false, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	now := nowText()
	id := newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, email, password_hash, role, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'admin', 'active', ?, ?)`, id, strings.ToLower(username), strings.ToLower(email), string(hash), now, now); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles(user_id, nickname, updated_at) VALUES(?, ?, ?)`, id, nickname, now); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, created_at) VALUES(?, 'admin.bootstrap', 'user', ?, ?)`, id, id, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) Register(ctx context.Context, input RegisterInput, ip string) (User, error) {
	if err := validateAccount(input.Username, input.Email, input.Password, input.Nickname); err != nil {
		return User{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var inviteID string
	var maxUses, useCount int
	var expiresText string
	var disabled sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, max_uses, use_count, expires_at, disabled_at FROM invites WHERE code_hash = ?`, hashValue(normalizeInvite(input.InviteCode))).Scan(&inviteID, &maxUses, &useCount, &expiresText, &disabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrInviteInvalid
		}
		return User{}, err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresText)
	if err != nil || disabled.Valid || useCount >= maxUses || !expires.After(now) {
		return User{}, ErrInviteInvalid
	}

	id := newID()
	nowString := now.Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO users(id, username, email, password_hash, role, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'member', 'active', ?, ?)`, id, strings.ToLower(input.Username), strings.ToLower(input.Email), string(passwordHash), nowString, nowString)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles(user_id, nickname, updated_at) VALUES(?, ?, ?)`, id, strings.TrimSpace(input.Nickname), nowString); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET use_count = use_count + 1 WHERE id = ? AND use_count < max_uses`, inviteID); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'user.register', 'user', ?, ?, ?)`, id, id, ip, nowString); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}

func (s *Store) Authenticate(ctx context.Context, identifier, password string) (User, error) {
	var user User
	var passwordHash string
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.email, u.password_hash, u.role, u.status,
		p.nickname, p.bio, p.bed_no, p.memorial_note, p.avatar_path
		FROM users u JOIN profiles p ON p.user_id = u.id
		WHERE u.username = ? COLLATE NOCASE OR u.email = ? COLLATE NOCASE`, strings.TrimSpace(identifier), strings.TrimSpace(identifier)).Scan(
		&user.ID, &user.Username, &user.Email, &passwordHash, &user.Role, &user.Status,
		&user.Nickname, &user.Bio, &user.BedNo, &user.MemorialNote, &user.AvatarPath,
	)
	if err != nil || user.Status != "active" || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.email, u.role, u.status,
		p.nickname, p.bio, p.bed_no, p.memorial_note, p.avatar_path
		FROM users u JOIN profiles p ON p.user_id = u.id WHERE u.id = ?`, id).Scan(
		&user.ID, &user.Username, &user.Email, &user.Role, &user.Status,
		&user.Nickname, &user.Bio, &user.BedNo, &user.MemorialNote, &user.AvatarPath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) CreateSession(ctx context.Context, userID, userAgent, ip string, ttl time.Duration) (token string, sessionID string, expires time.Time, err error) {
	token, err = randomToken(32)
	if err != nil {
		return "", "", time.Time{}, err
	}
	sessionID = newID()
	now := time.Now().UTC()
	expires = now.Add(ttl)
	_, err = s.db.ExecContext(ctx, `INSERT INTO sessions(id, token_hash, user_id, user_agent, ip_address, created_at, last_seen_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, sessionID, hashValue(token), userID, limit(userAgent, 512), limit(ip, 128), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano))
	return token, sessionID, expires, err
}

func (s *Store) UserForToken(ctx context.Context, token string) (User, string, error) {
	if token == "" {
		return User{}, "", ErrInvalidCredentials
	}
	var userID, sessionID, expiresText string
	var revoked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT user_id, id, expires_at, revoked_at FROM sessions WHERE token_hash = ?`, hashValue(token)).Scan(&userID, &sessionID, &expiresText, &revoked)
	if err != nil {
		return User{}, "", ErrInvalidCredentials
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresText)
	if err != nil || revoked.Valid || !expires.After(time.Now().UTC()) {
		return User{}, "", ErrInvalidCredentials
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil || user.Status != "active" {
		return User{}, "", ErrInvalidCredentials
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE id = ?", nowText(), sessionID)
	return user, sessionID, nil
}

func (s *Store) RevokeSession(ctx context.Context, ownerID, sessionID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, nowText(), sessionID, ownerID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListSessions(ctx context.Context, userID, currentID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_agent, ip_address, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ? ORDER BY last_seen_at DESC`, userID, nowText())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var item Session
		var created, seen, expires string
		if err := rows.Scan(&item.ID, &item.UserAgent, &item.IPAddress, &created, &seen, &expires); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		item.LastSeenAt, _ = time.Parse(time.RFC3339Nano, seen)
		item.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		item.Current = item.ID == currentID
		sessions = append(sessions, item)
	}
	return sessions, rows.Err()
}

func (s *Store) CreateInvite(ctx context.Context, actor User, maxUses int, ttl time.Duration, ip string) (string, time.Time, error) {
	if actor.Role != "admin" {
		return "", time.Time{}, ErrForbidden
	}
	if maxUses < 1 || maxUses > 20 || ttl < time.Minute || ttl > 90*24*time.Hour {
		return "", time.Time{}, errors.New("invalid invite limits")
	}
	raw, err := randomBytes(10)
	if err != nil {
		return "", time.Time{}, err
	}
	code := strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "=")
	expires := time.Now().UTC().Add(ttl)
	_, err = s.db.ExecContext(ctx, `INSERT INTO invites(id, code_hash, created_by, max_uses, expires_at, created_at) VALUES(?, ?, ?, ?, ?, ?)`, newID(), hashValue(code), actor.ID, maxUses, expires.Format(time.RFC3339Nano), nowText())
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, metadata_json, ip_address, created_at) VALUES(?, 'invite.create', 'invite', ?, ?, ?)`, actor.ID, fmt.Sprintf(`{"max_uses":%d}`, maxUses), ip, nowText())
	}
	return code, expires, err
}

func (s *Store) UpdateProfile(ctx context.Context, userID string, input ProfileInput, ip string) (User, error) {
	input.Nickname = strings.TrimSpace(input.Nickname)
	if len([]rune(input.Nickname)) < 1 || len([]rune(input.Nickname)) > 40 || len([]rune(input.Bio)) > 500 || len([]rune(input.MemorialNote)) > 500 || len([]rune(input.BedNo)) > 30 {
		return User{}, errors.New("invalid profile fields")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE profiles SET nickname = ?, bio = ?, bed_no = ?, memorial_note = ?, updated_at = ? WHERE user_id = ?`, input.Nickname, input.Bio, input.BedNo, input.MemorialNote, nowText(), userID)
	if err != nil {
		return User{}, err
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'profile.update', 'user', ?, ?, ?)`, userID, userID, ip, nowText())
	return s.UserByID(ctx, userID)
}

func (s *Store) SetAvatar(ctx context.Context, userID, mediaID, ip string) (User, string, error) {
	var valid int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE id = ? AND owner_id = ? AND media_type = 'image' AND status = 'ready')`, mediaID, userID).Scan(&valid); err != nil {
		return User{}, "", err
	}
	if valid == 0 {
		return User{}, "", ErrForbidden
	}
	var previous string
	if err := s.db.QueryRowContext(ctx, "SELECT avatar_path FROM profiles WHERE user_id = ?", userID).Scan(&previous); err != nil {
		return User{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE profiles SET avatar_path = ?, updated_at = ? WHERE user_id = ?", mediaID, nowText(), userID); err != nil {
		return User{}, "", err
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'profile.avatar', 'user', ?, ?, ?)`, userID, userID, ip, nowText())
	user, err := s.UserByID(ctx, userID)
	return user, previous, err
}

func (s *Store) ClearAvatar(ctx context.Context, userID, ip string) (User, string, error) {
	var previous string
	if err := s.db.QueryRowContext(ctx, "SELECT avatar_path FROM profiles WHERE user_id = ?", userID).Scan(&previous); err != nil {
		return User{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE profiles SET avatar_path = '', updated_at = ? WHERE user_id = ?", nowText(), userID); err != nil {
		return User{}, "", err
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'profile.avatar.clear', 'user', ?, ?, ?)`, userID, userID, ip, nowText())
	user, err := s.UserByID(ctx, userID)
	return user, previous, err
}

func validateAccount(username, email, password, nickname string) error {
	if !usernamePattern.MatchString(strings.TrimSpace(username)) {
		return errors.New("username must be 3-24 letters, numbers, underscores, or hyphens")
	}
	parsedEmail, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || parsedEmail.Address != strings.TrimSpace(email) || !strings.Contains(email, "@") {
		return errors.New("invalid email")
	}
	if len(password) < 10 || len(password) > 128 {
		return errors.New("password must be 10-128 characters")
	}
	if n := len([]rune(strings.TrimSpace(nickname))); n < 1 || n > 40 {
		return errors.New("nickname must be 1-40 characters")
	}
	return nil
}

func newID() string {
	b, err := randomBytes(16)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func randomToken(size int) (string, error) {
	b, err := randomBytes(size)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomBytes(size int) ([]byte, error) {
	b := make([]byte, size)
	_, err := rand.Read(b)
	return b, err
}

func hashValue(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func normalizeInvite(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }
func nowText() string                    { return time.Now().UTC().Format(time.RFC3339Nano) }

func limit(value string, length int) string {
	runes := []rune(value)
	if len(runes) <= length {
		return value
	}
	return string(runes[:length])
}
