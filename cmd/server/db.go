package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

const schema = `
CREATE TABLE IF NOT EXISTS files (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	path       TEXT    NOT NULL UNIQUE,
	filename   TEXT    NOT NULL,
	folder     TEXT    NOT NULL DEFAULT '',
	file_type  TEXT    NOT NULL,
	width      INTEGER NOT NULL DEFAULT 0,
	height     INTEGER NOT NULL DEFAULT 0,
	size       INTEGER NOT NULL DEFAULT 0,
	file_mtime INTEGER NOT NULL,
	date_taken INTEGER NOT NULL,
	indexed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_folder     ON files(folder);
CREATE INDEX IF NOT EXISTS idx_files_file_type  ON files(file_type);
CREATE INDEX IF NOT EXISTS idx_files_date_taken ON files(date_taken);
CREATE INDEX IF NOT EXISTS idx_files_file_mtime ON files(file_mtime);
CREATE INDEX IF NOT EXISTS idx_files_filename   ON files(filename);

CREATE TABLE IF NOT EXISTS file_views (
	file_path      TEXT    NOT NULL,
	user_id        TEXT    NOT NULL DEFAULT '',
	view_count     INTEGER NOT NULL DEFAULT 0,
	last_viewed_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (file_path, user_id)
);
CREATE INDEX IF NOT EXISTS idx_file_views_user_recent ON file_views(user_id, last_viewed_at DESC);
CREATE INDEX IF NOT EXISTS idx_file_views_user_count  ON file_views(user_id, view_count    DESC);

CREATE TABLE IF NOT EXISTS file_favorites (
	file_path    TEXT    NOT NULL,
	user_id      TEXT    NOT NULL DEFAULT '',
	favorited_at INTEGER NOT NULL,
	PRIMARY KEY (file_path, user_id)
);
CREATE INDEX IF NOT EXISTS idx_file_favorites_user ON file_favorites(user_id, favorited_at DESC);
`

func openDB(dbDir string) (*sql.DB, error) {
	var dbPath string
	if dbDir == ":memory:" {
		dbPath = ":memory:"
	} else {
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return nil, fmt.Errorf("openDB: mkdir %s: %w", dbDir, err)
		}
		dbPath = filepath.Join(dbDir, "files.db")
	}
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("openDB: %w", err)
	}
	// Single connection serializes all writes through Go's pool mutex,
	// preventing SQLITE_BUSY from concurrent goroutines.
	d.SetMaxOpenConns(1)
	if _, err := d.Exec("PRAGMA journal_mode=WAL"); err != nil {
		d.Close()
		return nil, fmt.Errorf("openDB: WAL: %w", err)
	}
	if _, err := d.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		d.Close()
		return nil, fmt.Errorf("openDB: synchronous: %w", err)
	}
	if _, err := d.Exec("PRAGMA busy_timeout=5000"); err != nil {
		d.Close()
		return nil, fmt.Errorf("openDB: busy_timeout: %w", err)
	}
	if _, err := d.Exec(schema); err != nil {
		d.Close()
		return nil, fmt.Errorf("openDB: schema: %w", err)
	}
	return d, nil
}

// dbRead is a pool of read-only connections so list/browse queries don't
// queue behind writes (or each other) on the single writer connection — WAL
// lets readers run concurrently with the writer. nil in tests, where the
// in-memory database can't be shared across connections; use readDB().
var dbRead *sql.DB

const readPoolSize = 4

// openReadDB opens the read pool for the database in dbDir. Call after
// openDB, which creates the schema and switches the file to WAL mode.
// Pragmas are set through the DSN so they apply to every pooled connection.
func openReadDB(dbDir string) (*sql.DB, error) {
	dsn := filepath.Join(dbDir, "files.db") + "?_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("openReadDB: %w", err)
	}
	d.SetMaxOpenConns(readPoolSize)
	d.SetMaxIdleConns(readPoolSize)
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("openReadDB: %w", err)
	}
	return d, nil
}

// readDB returns the pool to use for read-only queries: the read pool when
// open, otherwise the writer.
func readDB() *sql.DB {
	if dbRead != nil {
		return dbRead
	}
	return db
}

// upsertFile inserts or updates a file record. When w==0 the existing width/height
// in the database are preserved (used when dimensions are not yet known).
func upsertFile(imgPath string, fi os.FileInfo, dateTaken time.Time, w, h int) error {
	folder := path.Dir(imgPath)
	if folder == "." {
		folder = ""
	}
	fileType := "image"
	if isVideo(imgPath) {
		fileType = "video"
	}
	_, err := db.Exec(`
		INSERT INTO files (path, filename, folder, file_type, width, height, size, file_mtime, date_taken, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			filename   = excluded.filename,
			folder     = excluded.folder,
			file_type  = excluded.file_type,
			width      = CASE WHEN excluded.width > 0 THEN excluded.width ELSE width END,
			height     = CASE WHEN excluded.height > 0 THEN excluded.height ELSE height END,
			size       = excluded.size,
			file_mtime = excluded.file_mtime,
			date_taken = excluded.date_taken,
			indexed_at = excluded.indexed_at`,
		imgPath, filepath.Base(imgPath), folder, fileType, w, h, fi.Size(),
		fi.ModTime().UnixNano(), dateTaken.UnixNano(), time.Now().UnixNano(),
	)
	return err
}

// deleteFile removes a file record from the database. Also clears any view
// history and favorite rows for the path — those are keyed by path and would
// otherwise linger as phantom rows. All three deletes commit atomically.
func deleteFile(imgPath string) {
	if err := deleteFileTx(imgPath); err != nil {
		log.Printf("db: delete %s: %v", imgPath, err)
	}
}

func deleteFileTx(imgPath string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM files WHERE path = ?`, imgPath); err != nil {
		return err
	}
	for _, t := range userDataTables {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE file_path = ?`, imgPath); err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
	}
	return tx.Commit()
}

// subtreeCond is a WHERE fragment matching rows whose folder is f or any
// folder beneath it. It uses the half-open range [f+"/", f+"0") — '0' is the
// byte after '/' — instead of LIKE, so it is index-backed and folder names
// containing % or _ match literally. Bind with subtreeArgs(f).
const subtreeCond = "(folder = ? OR (folder >= ? AND folder < ?))"

func subtreeArgs(f string) []any { return []any{f, f + "/", f + "0"} }

// likeEscaper escapes LIKE metacharacters for use with ESCAPE '\'.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// userDataTables are the per-user tables keyed by file_path.
var userDataTables = []string{"file_views", "file_favorites"}

// moveUserData re-keys view history and favorites from one path to another,
// atomically. Rows already present at the destination (orphans left by an
// earlier file at the same path) are replaced.
func moveUserData(from, to string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, t := range userDataTables {
		if _, err := tx.Exec(`UPDATE OR REPLACE `+t+` SET file_path = ? WHERE file_path = ?`, to, from); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// deleteFolderRecords removes every file record under folder (at any depth)
// together with its view and favorite rows, and returns the removed paths.
// Matching uses the half-open range [folder+"/", folder+"0") — '0' is the
// byte after '/' — so it is index-backed and immune to LIKE wildcards.
func deleteFolderRecords(folder string) ([]string, error) {
	lo, hi := folder+"/", folder+"0"
	rows, err := db.Query(`SELECT path FROM files WHERE path >= ? AND path < ?`, lo, hi)
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, p)
	}
	// Close before opening the transaction: the pool has a single connection.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM files WHERE path >= ? AND path < ?`, lo, hi); err != nil {
		return nil, err
	}
	for _, t := range userDataTables {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE file_path >= ? AND file_path < ?`, lo, hi); err != nil {
			return nil, err
		}
	}
	return paths, tx.Commit()
}

// setFavorite records or removes a favorite for (imgPath, userID). Insert is
// idempotent — re-favoriting is a no-op. Delete on a missing row is also fine.
func setFavorite(imgPath, userID string, fav bool) error {
	if fav {
		_, err := db.Exec(`
			INSERT INTO file_favorites (file_path, user_id, favorited_at)
			VALUES (?, ?, ?)
			ON CONFLICT(file_path, user_id) DO NOTHING`,
			imgPath, userID, time.Now().UnixNano())
		return err
	}
	_, err := db.Exec(`DELETE FROM file_favorites WHERE file_path = ? AND user_id = ?`, imgPath, userID)
	return err
}

// trackView records that user u viewed imgPath. When u is nil (feature
// disabled or unauthenticated route) the view is stored under an empty user id.
// Intended to be called from safeGo; errors are logged, not returned.
func trackView(imgPath string, u *ConfigUser) {
	uid := ""
	if u != nil {
		uid = u.ID
	}
	_, err := db.Exec(`
		INSERT INTO file_views (file_path, user_id, view_count, last_viewed_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(file_path, user_id) DO UPDATE SET
			view_count     = view_count + 1,
			last_viewed_at = excluded.last_viewed_at`,
		imgPath, uid, time.Now().UnixNano())
	if err != nil {
		log.Printf("trackView %s (%s): %v", imgPath, uid, err)
	}
}

// lookupFile loads a single ImageInfo by path. Returns sql.ErrNoRows if the
// record is missing. userID scopes the Favorited field to that user (empty
// string for the anonymous/no-users case).
func lookupFile(imgPath, userID string) (ImageInfo, error) {
	var (
		filename                   string
		mtimeNs, dateTakenNs, size int64
		w, h                       int
		favorited                  bool
	)
	err := readDB().QueryRow(
		`SELECT files.filename, files.file_mtime, files.date_taken, files.size,
		        files.width, files.height,
		        ff.favorited_at IS NOT NULL
		   FROM files
		   LEFT JOIN file_favorites ff ON ff.file_path = files.path AND ff.user_id = ?
		  WHERE files.path = ?`, userID, imgPath,
	).Scan(&filename, &mtimeNs, &dateTakenNs, &size, &w, &h, &favorited)
	if err != nil {
		return ImageInfo{}, err
	}
	mtime := time.Unix(0, mtimeNs)
	small, medium, original := thumbURLs(imgPath, mtime)
	return ImageInfo{
		Filename:    filename,
		Path:        imgPath,
		ModTime:     time.Unix(0, dateTakenNs),
		FileMtime:   mtime,
		Size:        size,
		Width:       w,
		Height:      h,
		ThumbSmall:  small,
		ThumbMedium: medium,
		Original:    original,
		Favorited:   favorited,
	}, nil
}

// queryParams holds all filters and pagination options for queryFiles.
type queryParams struct {
	folder           string   // relative folder path; "" = all files
	ftype            string   // "image", "video", or "" = all
	search           string   // substring match on filename
	year             int      // calendar year of date_taken (UTC); 0 = no filter
	ownerFolders     []string // top-level folders associated with a selected owner; nil/empty = no filter. A non-nil but empty slice means "owner matched no folders" and forces an empty result.
	sort             string   // "taken", "mtime", "name"
	order            string   // "asc" or "desc"
	page             int
	limit            int
	paginate         bool
	deniedTopFolders []string // top folders to exclude (any file under these is hidden); empty = no restriction
	viewedMode       string   // "" (off), "recent" (last_viewed_at DESC), "most" (view_count DESC). When set, INNER JOIN file_views scoped to userID — only viewed files are returned, and sort/order are ignored.
	favorited        bool     // when true, only rows favorited by userID are returned
	userID           string   // user id used to scope viewedMode/favorited/Favorited; "" for anonymous
}

// queryFiles runs a filtered, sorted, paginated query against the files table.
// It returns the requested page of ImageInfo and the total matching count.
func queryFiles(params queryParams) ([]ImageInfo, int, error) {
	var conds []string
	var args []any

	// Always LEFT JOIN file_favorites scoped to the current user so we can
	// surface the Favorited field on every row without an N+1. The join is a
	// keyed lookup on (file_path, user_id) — cheap.
	joinClause := "LEFT JOIN file_favorites ff ON ff.file_path = files.path AND ff.user_id = ?"
	args = append(args, params.userID)

	// When filtering by viewed mode, additionally INNER JOIN file_views scoped
	// to the current user. Its arg comes after the favorites-join arg.
	if params.viewedMode != "" {
		joinClause += " INNER JOIN file_views fv ON fv.file_path = files.path AND fv.user_id = ?"
		args = append(args, params.userID)
	}

	if params.favorited {
		conds = append(conds, "ff.favorited_at IS NOT NULL")
	}

	if params.folder != "" {
		conds = append(conds, subtreeCond)
		args = append(args, subtreeArgs(params.folder)...)
	}
	if params.ftype != "" {
		conds = append(conds, "file_type = ?")
		args = append(args, params.ftype)
	}
	if params.search != "" {
		parts := []string{`LOWER(filename) LIKE LOWER(?) ESCAPE '\'`}
		args = append(args, "%"+likeEscaper.Replace(params.search)+"%")
		if dq := parseDateQuery(params.search); dq != nil {
			for _, r := range dq.ranges {
				parts = append(parts, "(date_taken >= ? AND date_taken < ?)")
				args = append(args, r.startNs, r.endNs)
			}
			if len(dq.months) > 0 {
				// Season-only queries match a set of months across all years.
				// date_taken is UnixNano; divide to seconds for strftime.
				// Not indexable — acceptable at gallery scale.
				placeholders := make([]string, len(dq.months))
				for i, m := range dq.months {
					placeholders[i] = "?"
					args = append(args, m)
				}
				parts = append(parts, "CAST(strftime('%m', date_taken / 1000000000, 'unixepoch') AS INTEGER) IN ("+strings.Join(placeholders, ",")+")")
			}
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}
	if params.year > 0 {
		start := time.Date(params.year, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
		end := time.Date(params.year+1, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
		conds = append(conds, "date_taken >= ? AND date_taken < ?")
		args = append(args, start, end)
	}
	if params.ownerFolders != nil {
		if len(params.ownerFolders) == 0 {
			conds = append(conds, "1=0")
		} else {
			parts := make([]string, 0, len(params.ownerFolders))
			for _, f := range params.ownerFolders {
				parts = append(parts, subtreeCond)
				args = append(args, subtreeArgs(f)...)
			}
			conds = append(conds, "("+strings.Join(parts, " OR ")+")")
		}
	}
	for _, d := range params.deniedTopFolders {
		conds = append(conds, "NOT "+subtreeCond)
		args = append(args, subtreeArgs(d)...)
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var orderClause string
	switch params.viewedMode {
	case "recent":
		orderClause = "ORDER BY fv.last_viewed_at DESC, files.path ASC"
	case "most":
		orderClause = "ORDER BY fv.view_count DESC, fv.last_viewed_at DESC, files.path ASC"
	default:
		var orderCol string
		switch params.sort {
		case "name":
			orderCol = "path"
		case "mtime":
			orderCol = "file_mtime"
		default:
			orderCol = "date_taken"
		}
		dir := "DESC"
		if params.order == "asc" {
			dir = "ASC"
		}
		orderClause = fmt.Sprintf("ORDER BY %s %s, path ASC", orderCol, dir)
	}

	var total int
	if err := readDB().QueryRow(
		fmt.Sprintf("SELECT COUNT(*) FROM files %s %s", joinClause, where), args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitClause := ""
	if params.paginate {
		limitClause = fmt.Sprintf("LIMIT %d OFFSET %d", params.limit, (params.page-1)*params.limit)
	}

	q := fmt.Sprintf(
		"SELECT files.path, filename, file_mtime, date_taken, size, width, height, ff.favorited_at IS NOT NULL FROM files %s %s %s %s",
		joinClause, where, orderClause, limitClause,
	)
	rows, err := readDB().Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var images []ImageInfo
	for rows.Next() {
		var imgPath, filename string
		var mtimeNs, dateTakenNs, size int64
		var w, h int
		var favorited bool
		if err := rows.Scan(&imgPath, &filename, &mtimeNs, &dateTakenNs, &size, &w, &h, &favorited); err != nil {
			return nil, 0, err
		}
		mtime := time.Unix(0, mtimeNs)
		small, medium, original := thumbURLs(imgPath, mtime)
		images = append(images, ImageInfo{
			Filename:    filename,
			Path:        imgPath,
			ModTime:     time.Unix(0, dateTakenNs),
			FileMtime:   mtime,
			Size:        size,
			Width:       w,
			Height:      h,
			ThumbSmall:  small,
			ThumbMedium: medium,
			Original:    original,
			Favorited:   favorited,
		})
	}
	return images, total, rows.Err()
}
