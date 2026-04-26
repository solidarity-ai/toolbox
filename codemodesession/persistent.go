package codemodesession

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	repl "github.com/mackross/repljs"
	replsqlite "github.com/mackross/repljs/store/sqlite"
)

const (
	TBSessionParam      = "tb_session"
	IntentParam         = "intent"
	NewSessionToolName  = "new_super_tool_session"
	sessionsDirEnv      = "TOOLBOX_SESSIONS_DIR"
	tbSessionLength     = 6
	metadataSingletonID = 1
	leaseSingletonID    = 1
)

func NewSessionToolDescription(awaitAvailable bool) string {
	if awaitAvailable {
		return "Create a fresh notebook and return its tb_session. Reuse that same tb_session on later super_tool and await_super_tool_approvals calls."
	}
	return "Create a fresh notebook and return its tb_session. Reuse that same tb_session on later super_tool calls."
}

func normalizeSessionIntent(intent string) string {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return defaultSessionIntent
	}
	return intent
}

var (
	ErrTBSessionRequired    = errors.New("tb_session is required")
	ErrTBSessionExists      = errors.New("tb_session already exists")
	ErrTBSessionNotFound    = errors.New("tb_session not found")
	ErrTBSessionMismatch    = errors.New("tb_session does not match locked session")
	ErrSessionAlreadyActive = errors.New("session already active elsewhere")
	ErrSessionLeaseLost     = errors.New("session lease lost")

	errPersistentSessionState = errors.New("persistent session metadata is missing or invalid")

	tbSessionReader io.Reader        = rand.Reader
	leaseTTL        time.Duration    = 30 * time.Second
	leaseHeartbeat  time.Duration    = 10 * time.Second
	leaseNow        func() time.Time = func() time.Time { return time.Now().UTC() }
)

type persistentSessionMetadata struct {
	TBSession       string
	REPLSession     string
	IntentText      string
	IntentSource    string
	IntentUpdatedAt time.Time
	CreatedAt       time.Time
	LastOpenedAt    time.Time
}

const defaultSessionIntent = "Manual Toolbox session"

type sessionLease struct {
	db         *sql.DB
	tbSession  string
	ownerToken string
	pid        int
	hostname   string

	mu      sync.Mutex
	lostErr error
	closed  bool
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	onLost  func(error)
}

func ValidateTBSession(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrTBSessionRequired
	}
	if len(id) != tbSessionLength {
		return fmt.Errorf("tb_session must be %d lowercase hex characters", tbSessionLength)
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("tb_session must be %d lowercase hex characters", tbSessionLength)
		}
	}
	return nil
}

func GenerateTBSession() (string, error) {
	var raw [tbSessionLength / 2]byte
	if _, err := io.ReadFull(tbSessionReader, raw[:]); err != nil {
		return "", fmt.Errorf("generate tb_session: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func SessionsDir() (string, error) {
	dir := strings.TrimSpace(os.Getenv(sessionsDirEnv))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".toolbox", "sessions")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create sessions dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("chmod sessions dir: %w", err)
	}
	return dir, nil
}

func SessionDBPath(tbSession string) (string, error) {
	if err := ValidateTBSession(tbSession); err != nil {
		return "", err
	}
	dir, err := SessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, tbSession+".db"), nil
}

func CreateFresh(ctx context.Context, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	return CreateFreshWithIntent(ctx, currentDir, defaultSessionIntent, cfgs...)
}

func CreateFreshWithIntent(ctx context.Context, currentDir, intent string, cfgs ...SessionConfig) (*Session, error) {
	var lastErr error
	for attempt := 0; attempt < 32; attempt++ {
		tbSession, err := GenerateTBSession()
		if err != nil {
			return nil, err
		}
		session, err := CreateNewWithIntent(ctx, tbSession, currentDir, intent, cfgs...)
		if err == nil {
			return session, nil
		}
		if !errors.Is(err, ErrTBSessionExists) {
			return nil, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ErrTBSessionExists
	}
	return nil, fmt.Errorf("reserve fresh tb_session: %w", lastErr)
}

func CreateNew(ctx context.Context, tbSession, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	return CreateNewWithIntent(ctx, tbSession, currentDir, defaultSessionIntent, cfgs...)
}

func CreateNewWithIntent(ctx context.Context, tbSession, currentDir, intent string, cfgs ...SessionConfig) (*Session, error) {
	dbPath, err := reserveSessionDB(tbSession)
	if err != nil {
		return nil, err
	}
	return openPersistentSession(ctx, dbPath, tbSession, currentDir, normalizeSessionIntent(intent), firstConfig(cfgs), true)
}

func OpenExisting(ctx context.Context, tbSession, currentDir string, cfgs ...SessionConfig) (*Session, error) {
	dbPath, err := SessionDBPath(tbSession)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w %q", ErrTBSessionNotFound, tbSession)
		}
		return nil, fmt.Errorf("stat tb_session db: %w", err)
	}
	return openPersistentSession(ctx, dbPath, tbSession, currentDir, "", firstConfig(cfgs), false)
}

func reserveSessionDB(tbSession string) (string, error) {
	dbPath, err := SessionDBPath(tbSession)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("%w %q", ErrTBSessionExists, tbSession)
		}
		return "", fmt.Errorf("reserve tb_session db: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("chmod tb_session db: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close reserved tb_session db: %w", err)
	}
	return dbPath, nil
}

func openPersistentSession(ctx context.Context, dbPath, tbSession, currentDir, intent string, cfg SessionConfig, create bool) (*Session, error) {
	if err := ValidateTBSession(tbSession); err != nil {
		return nil, err
	}
	executor, ownExecutor := resolveExecutor(cfg.Executor, cfg.PreparedTools)

	st, err := replsqlite.Open(ctx, dbPath)
	if err != nil {
		if ownExecutor {
			_ = executor.Close()
		}
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	cleanupStore := func(err error) (*Session, error) {
		_ = st.Close()
		if ownExecutor {
			_ = executor.Close()
		}
		return nil, err
	}

	if err := ensurePersistentSessionSchema(ctx, st.DB()); err != nil {
		return cleanupStore(err)
	}
	approvals, err := newSQLiteApprovalStore(st.DB())
	if err != nil {
		return cleanupStore(fmt.Errorf("open approval store: %w", err))
	}
	toolCalls := newSQLiteToolCallJournal(st, dbPath)
	prepared := newPreparedState(cfg.PreparedTools)
	toolRuns := newToolRunState()
	deps := sessionDeps(currentDir, st, prepared.Get, toolCalls, approvals, executor, toolRuns.Acquire)

	lease, err := acquireSessionLease(ctx, st.DB(), tbSession)
	if err != nil {
		return cleanupStore(err)
	}
	cleanupLease := func(err error) (*Session, error) {
		_ = lease.Close()
		return cleanupStore(err)
	}

	var (
		sess    repl.Session
		resumed bool
		meta    persistentSessionMetadata
	)
	if create {
		sess, err = repl.New().StartSession(ctx, repl.SessionConfig{
			Manifest: repl.Manifest{ID: manifestID},
		}, deps)
		if err != nil {
			return cleanupLease(fmt.Errorf("start session: %w", err))
		}
		now := leaseNow()
		meta = persistentSessionMetadata{
			TBSession:       tbSession,
			REPLSession:     string(sess.ID()),
			IntentText:      normalizeSessionIntent(intent),
			IntentSource:    "user",
			IntentUpdatedAt: now,
			CreatedAt:       now,
			LastOpenedAt:    now,
		}
		if err := writeSessionMetadata(ctx, st.DB(), meta); err != nil {
			_ = sess.Close()
			return cleanupLease(err)
		}
	} else {
		loadedMeta, err := loadSessionMetadata(ctx, st.DB())
		if err != nil {
			return cleanupLease(err)
		}
		meta = loadedMeta
		if meta.TBSession != tbSession {
			return cleanupLease(fmt.Errorf("%w: requested %q, stored %q", errPersistentSessionState, tbSession, meta.TBSession))
		}
		sess, err = repl.New().OpenSession(ctx, repl.SessionID(meta.REPLSession), deps)
		if err != nil {
			return cleanupLease(fmt.Errorf("open session %q: %w", meta.REPLSession, err))
		}
		meta.LastOpenedAt = leaseNow()
		if strings.TrimSpace(meta.IntentText) == "" {
			meta.IntentText = defaultSessionIntent
			meta.IntentSource = "fallback"
			meta.IntentUpdatedAt = meta.LastOpenedAt
		}
		if err := writeSessionMetadata(ctx, st.DB(), meta); err != nil {
			_ = sess.Close()
			return cleanupLease(err)
		}
		resumed = true
	}

	if err := transitionSessionPreparedTools(ctx, sess, prepared.Get()); err != nil {
		_ = sess.Close()
		return cleanupLease(fmt.Errorf("apply prepared tools: %w", err))
	}
	if resumed {
		if approvals != nil {
			if err := approvals.RecoverSession(ctx, sess.ID(), toolCalls); err != nil {
				_ = sess.Close()
				return cleanupLease(fmt.Errorf("recover executing approvals: %w", err))
			}
		}
		if toolCalls != nil {
			if err := toolCalls.Recover(ctx, sess.ID()); err != nil {
				_ = sess.Close()
				return cleanupLease(fmt.Errorf("recover tool calls: %w", err))
			}
		}
	}

	session := &Session{
		session:         sess,
		store:           st,
		storeCloser:     st,
		id:              sess.ID(),
		tbSession:       tbSession,
		intentText:      normalizeSessionIntent(meta.IntentText),
		intentSource:    strings.TrimSpace(meta.IntentSource),
		intentUpdatedAt: meta.IntentUpdatedAt,
		resumed:         resumed,
		prepared:        prepared,
		applied:         prepared.Get(),
		toolCalls:       toolCalls,
		approvals:       approvals,
		approvalAwaits:  newApprovalAwaitDelegate(),
		toolRuns:        toolRuns,
		executor:        executor,
		ownExecutor:     ownExecutor,
		lease:           lease,
	}
	lease.start(func(err error) {
		session.markTerminal(err)
	})
	return session, nil
}

func ensurePersistentSessionSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS toolbox_session_metadata (
  singleton_id    INTEGER PRIMARY KEY CHECK (singleton_id = 1),
  tb_session      TEXT NOT NULL,
  repl_session_id TEXT NOT NULL,
  intent_text     TEXT NOT NULL DEFAULT '',
  intent_source   TEXT NOT NULL DEFAULT '',
  intent_updated_at TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  last_opened_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS session_lease (
  singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
  owner_token  TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  pid          INTEGER NOT NULL DEFAULT 0,
  hostname     TEXT NOT NULL DEFAULT '',
  updated_at   TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("migrate persistent session schema: %w", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE toolbox_session_metadata ADD COLUMN intent_text TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE toolbox_session_metadata ADD COLUMN intent_source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE toolbox_session_metadata ADD COLUMN intent_updated_at TEXT NOT NULL DEFAULT ''`,
	} {
		if _, alterErr := db.ExecContext(ctx, stmt); alterErr != nil && !strings.Contains(alterErr.Error(), "duplicate column name") {
			return fmt.Errorf("migrate persistent session schema: %w", alterErr)
		}
	}
	return nil
}

func loadSessionMetadata(ctx context.Context, db *sql.DB) (persistentSessionMetadata, error) {
	row := db.QueryRowContext(ctx, `
SELECT tb_session, repl_session_id, intent_text, intent_source, intent_updated_at, created_at, last_opened_at
FROM toolbox_session_metadata
WHERE singleton_id = ?`,
		metadataSingletonID,
	)
	var meta persistentSessionMetadata
	var intentUpdatedAt, createdAt, lastOpenedAt string
	if err := row.Scan(&meta.TBSession, &meta.REPLSession, &meta.IntentText, &meta.IntentSource, &intentUpdatedAt, &createdAt, &lastOpenedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistentSessionMetadata{}, errPersistentSessionState
		}
		return persistentSessionMetadata{}, fmt.Errorf("load persistent session metadata: %w", err)
	}
	meta.IntentUpdatedAt = parseLeaseTime(intentUpdatedAt)
	meta.CreatedAt = parseLeaseTime(createdAt)
	meta.LastOpenedAt = parseLeaseTime(lastOpenedAt)
	if meta.TBSession == "" || meta.REPLSession == "" {
		return persistentSessionMetadata{}, errPersistentSessionState
	}
	return meta, nil
}

func writeSessionMetadata(ctx context.Context, db *sql.DB, meta persistentSessionMetadata) error {
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = leaseNow()
	}
	meta.IntentText = normalizeSessionIntent(meta.IntentText)
	if strings.TrimSpace(meta.IntentSource) == "" {
		meta.IntentSource = "fallback"
	}
	if meta.IntentUpdatedAt.IsZero() {
		meta.IntentUpdatedAt = meta.CreatedAt
	}
	if meta.LastOpenedAt.IsZero() {
		meta.LastOpenedAt = leaseNow()
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO toolbox_session_metadata
  (singleton_id, tb_session, repl_session_id, intent_text, intent_source, intent_updated_at, created_at, last_opened_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(singleton_id) DO UPDATE SET
  tb_session = excluded.tb_session,
  repl_session_id = excluded.repl_session_id,
  intent_text = excluded.intent_text,
  intent_source = excluded.intent_source,
  intent_updated_at = excluded.intent_updated_at,
  created_at = excluded.created_at,
  last_opened_at = excluded.last_opened_at`,
		metadataSingletonID,
		meta.TBSession,
		meta.REPLSession,
		meta.IntentText,
		meta.IntentSource,
		meta.IntentUpdatedAt.Format(time.RFC3339Nano),
		meta.CreatedAt.Format(time.RFC3339Nano),
		meta.LastOpenedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("write persistent session metadata: %w", err)
	}
	return nil
}

func acquireSessionLease(ctx context.Context, db *sql.DB, tbSession string) (*sessionLease, error) {
	token, err := randomHex(12)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	lease := &sessionLease{
		db:         db,
		tbSession:  tbSession,
		ownerToken: token,
		pid:        os.Getpid(),
		hostname:   strings.TrimSpace(host),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	if err := lease.acquire(ctx); err != nil {
		return nil, err
	}
	return lease, nil
}

func (l *sessionLease) start(onLost func(error)) {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.closed || l.started {
		l.mu.Unlock()
		return
	}
	l.onLost = onLost
	l.started = true
	l.mu.Unlock()
	go l.heartbeatLoop()
}

func (l *sessionLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	started := l.started
	l.closed = true
	close(l.stopCh)
	l.mu.Unlock()
	if started {
		<-l.doneCh
	}
	return l.release(context.Background())
}

func (l *sessionLease) ensureOwned(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.lostErr != nil {
		err := l.lostErr
		l.mu.Unlock()
		return err
	}
	if l.closed {
		l.mu.Unlock()
		return newLeaseLostError(l.tbSession, errors.New("session closed"))
	}
	l.mu.Unlock()
	return l.verify(ctx)
}

func (l *sessionLease) heartbeatLoop() {
	ticker := time.NewTicker(leaseHeartbeat)
	defer ticker.Stop()
	defer close(l.doneCh)

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			if err := l.renew(context.Background()); err != nil {
				l.markLost(err)
				return
			}
		}
	}
}

func (l *sessionLease) acquire(ctx context.Context) error {
	return l.upsert(ctx, false)
}

func (l *sessionLease) renew(ctx context.Context) error {
	return l.upsert(ctx, true)
}

func (l *sessionLease) upsert(ctx context.Context, allowSameOwner bool) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin lease tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	row := tx.QueryRowContext(ctx, `
SELECT owner_token, expires_at
FROM session_lease
WHERE singleton_id = ?`,
		leaseSingletonID,
	)
	var ownerToken, expiresAtRaw string
	switch err := row.Scan(&ownerToken, &expiresAtRaw); {
	case errors.Is(err, sql.ErrNoRows):
		now := leaseNow()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO session_lease
  (singleton_id, owner_token, expires_at, pid, hostname, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			leaseSingletonID,
			l.ownerToken,
			now.Add(leaseTTL).Format(time.RFC3339Nano),
			l.pid,
			l.hostname,
			now.Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("insert session lease: %w", err)
		}
	case err != nil:
		return fmt.Errorf("load session lease: %w", err)
	default:
		now := leaseNow()
		expiresAt := parseLeaseTime(expiresAtRaw)
		if allowSameOwner && ownerToken == l.ownerToken {
			if _, err := tx.ExecContext(ctx, `
UPDATE session_lease
SET expires_at = ?, pid = ?, hostname = ?, updated_at = ?
WHERE singleton_id = ? AND owner_token = ?`,
				now.Add(leaseTTL).Format(time.RFC3339Nano),
				l.pid,
				l.hostname,
				now.Format(time.RFC3339Nano),
				leaseSingletonID,
				l.ownerToken,
			); err != nil {
				return fmt.Errorf("renew session lease: %w", err)
			}
		} else if expiresAt.After(now) {
			return fmt.Errorf("%w for tb_session %q", ErrSessionAlreadyActive, l.tbSession)
		} else {
			if _, err := tx.ExecContext(ctx, `
UPDATE session_lease
SET owner_token = ?, expires_at = ?, pid = ?, hostname = ?, updated_at = ?
WHERE singleton_id = ?`,
				l.ownerToken,
				now.Add(leaseTTL).Format(time.RFC3339Nano),
				l.pid,
				l.hostname,
				now.Format(time.RFC3339Nano),
				leaseSingletonID,
			); err != nil {
				return fmt.Errorf("take over session lease: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit lease tx: %w", err)
	}
	return nil
}

func (l *sessionLease) verify(ctx context.Context) error {
	row := l.db.QueryRowContext(ctx, `
SELECT owner_token, expires_at
FROM session_lease
WHERE singleton_id = ?`,
		leaseSingletonID,
	)
	var ownerToken, expiresAtRaw string
	if err := row.Scan(&ownerToken, &expiresAtRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = newLeaseLostError(l.tbSession, errors.New("lease row disappeared"))
			l.markLost(err)
			return err
		}
		err = newLeaseLostError(l.tbSession, err)
		l.markLost(err)
		return err
	}
	if ownerToken != l.ownerToken {
		err := newLeaseLostError(l.tbSession, errors.New("ownership changed"))
		l.markLost(err)
		return err
	}
	if parseLeaseTime(expiresAtRaw).Before(leaseNow()) {
		err := newLeaseLostError(l.tbSession, errors.New("lease expired"))
		l.markLost(err)
		return err
	}
	return nil
}

func (l *sessionLease) release(ctx context.Context) error {
	if l == nil || l.db == nil {
		return nil
	}
	_, err := l.db.ExecContext(ctx, `
DELETE FROM session_lease
WHERE singleton_id = ? AND owner_token = ?`,
		leaseSingletonID,
		l.ownerToken,
	)
	if err != nil {
		return fmt.Errorf("release session lease: %w", err)
	}
	return nil
}

func (l *sessionLease) markLost(err error) {
	if l == nil || err == nil {
		return
	}
	l.mu.Lock()
	if l.lostErr != nil {
		l.mu.Unlock()
		return
	}
	if !errors.Is(err, ErrSessionLeaseLost) {
		err = newLeaseLostError(l.tbSession, err)
	}
	l.lostErr = err
	callback := l.onLost
	l.mu.Unlock()
	if callback != nil {
		callback(err)
	}
}

func newLeaseLostError(tbSession string, cause error) error {
	message := fmt.Sprintf("tb_session %q", tbSession)
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message += ": " + strings.TrimSpace(cause.Error())
	}
	return fmt.Errorf("%w: %s", ErrSessionLeaseLost, message)
}

func parseLeaseTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
