package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// cursorHeaderValueLimit bounds one header's JSON. A row over it is skipped: it is not
	// worth stalling the scan for
	cursorHeaderValueLimit = 1 << 20
	// cursorQueryTimeout is how long one read may take. Cursor holds the database while it
	// works, and a scan must not wait on it
	cursorQueryTimeout = 250 * time.Millisecond
	// cursorBusyTimeoutMs is the driver's own wait for a lock, shorter than the query timeout
	cursorBusyTimeoutMs = 100
)

// ErrCursorLayoutUnknown says the database doesn't have the table or columns this build knows.
// It is not the same as "no sessions": it means spark can't tell
var ErrCursorLayoutUnknown = errors.New("cursor composer layout unknown")

// CursorUserDir is the Cursor profile this build reads. Insiders and other profiles are out of
// scope on purpose
func CursorUserDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User")
}

// CursorGlobalDBPath is where Cursor keeps the composer headers the Agents window lists
func CursorGlobalDBPath(home string) string {
	return filepath.Join(CursorUserDir(home), "globalStorage", "state.vscdb")
}

// CursorWorkspaceStorageRoot holds one directory per workspace, each naming its folder
func CursorWorkspaceStorageRoot(home string) string {
	return filepath.Join(CursorUserDir(home), "workspaceStorage")
}

// cursorHeader is one desktop agent as the metadata describes it. Optional fields stay
// pointers: a missing `hasUnreadMessages` is not "read"
type cursorHeader struct {
	ID             string
	Title          string
	WorkspaceID    string
	WorkspaceRoots []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Unread         *bool
	// LocalDesktop is true for an agent whose location record says local, or which predates
	// the location field; cloud and background agents are excluded before this point
	LocalDesktop bool
}

// cursorHeaderValue is the whitelisted part of a composer header's JSON. Unknown fields are
// dropped by the decoder, and every field is optional
type cursorHeaderValue struct {
	Name          *string `json:"name"`
	Unread        *bool   `json:"hasUnreadMessages"`
	IsDraft       *bool   `json:"isDraft"`
	IsArchived    *bool   `json:"isArchived"`
	IsSpec        *bool   `json:"isSpec"`
	IsProject     *bool   `json:"isProject"`
	IsBestOfN     *bool   `json:"isBestOfNSubcomposer"`
	AgentLocation *struct {
		Type   *string `json:"type"`
		Status *string `json:"status"`
	} `json:"agentLocation"`
	WorkspaceIdentifier *struct {
		ID  *string `json:"id"`
		URI *struct {
			Scheme *string `json:"scheme"`
			Path   *string `json:"path"`
		} `json:"uri"`
	} `json:"workspaceIdentifier"`
}

// cursorHeaderColumns are the columns this build reads. A layout missing any of them is one
// spark doesn't know, and guessing past that would show unfiltered conversations
var cursorHeaderColumns = []string{
	"composerId", "workspaceId", "createdAt", "lastUpdatedAt", "isArchived", "isSubagent", "value",
}

// ReadCursorHeaders reads the modern composer headers. The database is opened read-only, and a
// path that doesn't exist is an error rather than an empty database
func ReadCursorHeaders(dbPath string, now time.Time) ([]cursorHeader, error) {
	db, err := openCursorDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cursorQueryTimeout)
	defer cancel()

	if err := checkCursorHeaderLayout(ctx, db); err != nil {
		return nil, err
	}

	query := `SELECT composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent, value
		FROM composerHeaders`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("reading composer headers: %w", err)
	}
	defer rows.Close()

	var headers []cursorHeader
	for rows.Next() {
		var (
			id, workspaceID, value sql.NullString
			createdAt, updatedAt   sql.NullInt64
			archived, subagent     sql.NullInt64
		)
		if err := rows.Scan(&id, &workspaceID, &createdAt, &updatedAt, &archived, &subagent, &value); err != nil {
			// One unreadable row loses itself and nothing else
			continue
		}
		if !value.Valid || len(value.String) > cursorHeaderValueLimit {
			continue
		}
		if archived.Int64 != 0 || subagent.Int64 != 0 {
			continue
		}
		header, ok := cursorHeaderFromValue(id.String, workspaceID.String, value.String, createdAt.Int64, updatedAt.Int64)
		if !ok {
			continue
		}
		headers = append(headers, header)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return headers, nil
}

// cursorHeaderFromValue decides whether one row is a desktop conversation the list is about
func cursorHeaderFromValue(id, workspaceID, raw string, createdAt, updatedAt int64) (cursorHeader, bool) {
	if strings.TrimSpace(id) == "" {
		return cursorHeader{}, false
	}
	var v cursorHeaderValue
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return cursorHeader{}, false
	}
	if truthy(v.IsArchived) || truthy(v.IsDraft) || truthy(v.IsSpec) || truthy(v.IsProject) || truthy(v.IsBestOfN) {
		return cursorHeader{}, false
	}
	if v.AgentLocation != nil && v.AgentLocation.Type != nil && *v.AgentLocation.Type != "local" {
		// A cloud or background agent isn't on this machine's desktop
		return cursorHeader{}, false
	}
	header := cursorHeader{
		ID:           id,
		WorkspaceID:  workspaceID,
		Title:        "Untitled Session",
		CreatedAt:    timestampTime(createdAt),
		UpdatedAt:    timestampTime(updatedAt),
		Unread:       v.Unread,
		LocalDesktop: true,
	}
	if v.Name != nil && strings.TrimSpace(*v.Name) != "" {
		header.Title = strings.TrimSpace(*v.Name)
	}
	if wi := v.WorkspaceIdentifier; wi != nil && wi.URI != nil && wi.URI.Path != nil {
		path := strings.TrimSpace(*wi.URI.Path)
		if path != "" && (wi.URI.Scheme == nil || *wi.URI.Scheme == "file") {
			header.WorkspaceRoots = []string{filepath.Clean(path)}
		}
	}
	return header, true
}

func truthy(b *bool) bool { return b != nil && *b }

// openCursorDB opens one database read-only with a single connection. Nothing here creates,
// migrates or writes: the file belongs to Cursor
func openCursorDB(path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return nil, fmt.Errorf("%s: %w", path, os.ErrNotExist)
	case err != nil:
		return nil, err
	case info.IsDir():
		return nil, fmt.Errorf("%s is a directory", path)
	}
	u := url.URL{Scheme: "file", Path: path}
	dsn := u.String() + fmt.Sprintf("?mode=ro&_pragma=busy_timeout(%d)", cursorBusyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// checkCursorHeaderLayout asks the schema what it has, so a version spark doesn't know is
// reported instead of read as an empty list
func checkCursorHeaderLayout(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(composerHeaders)`)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCursorLayoutUnknown, err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			continue
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrCursorLayoutUnknown, err)
	}
	for _, col := range cursorHeaderColumns {
		if !have[col] {
			return fmt.Errorf("%w: composerHeaders has no %s column", ErrCursorLayoutUnknown, col)
		}
	}
	return nil
}

// ReadCursorWorkspaceRoots maps workspace IDs to folder paths through the per-workspace
// workspace.json. Headers that predate the inline workspace identifier need it
func ReadCursorWorkspaceRoots(userDir string) map[string]string {
	root := filepath.Join(userDir, "workspaceStorage")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, e.Name(), "workspace.json"))
		if err != nil {
			continue
		}
		var file struct {
			Folder string `json:"folder"`
		}
		if json.Unmarshal(body, &file) != nil || file.Folder == "" {
			continue
		}
		u, err := url.Parse(file.Folder)
		if err != nil || u.Path == "" {
			continue
		}
		out[e.Name()] = filepath.Clean(u.Path)
	}
	return out
}
