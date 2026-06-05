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

// deleteFile removes a file record from the database.
func deleteFile(imgPath string) {
	if _, err := db.Exec(`DELETE FROM files WHERE path = ?`, imgPath); err != nil {
		log.Printf("db: delete %s: %v", imgPath, err)
	}
}

// queryParams holds all filters and pagination options for queryFiles.
type queryParams struct {
	folder   string // relative folder path; "" = all files
	ftype    string // "image", "video", or "" = all
	search   string // substring match on filename
	sort     string // "taken", "mtime", "name"
	order    string // "asc" or "desc"
	page     int
	limit    int
	paginate bool
}

// queryFiles runs a filtered, sorted, paginated query against the files table.
// It returns the requested page of ImageInfo and the total matching count.
func queryFiles(params queryParams) ([]ImageInfo, int, error) {
	var conds []string
	var args []any

	if params.folder != "" {
		conds = append(conds, "(folder = ? OR folder LIKE ?||'/%')")
		args = append(args, params.folder, params.folder)
	}
	if params.ftype != "" {
		conds = append(conds, "file_type = ?")
		args = append(args, params.ftype)
	}
	if params.search != "" {
		conds = append(conds, "LOWER(filename) LIKE LOWER(?)")
		args = append(args, "%"+params.search+"%")
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

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
	orderClause := fmt.Sprintf("ORDER BY %s %s, path ASC", orderCol, dir)

	var total int
	if err := db.QueryRow(
		fmt.Sprintf("SELECT COUNT(*) FROM files %s", where), args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitClause := ""
	if params.paginate {
		limitClause = fmt.Sprintf("LIMIT %d OFFSET %d", params.limit, (params.page-1)*params.limit)
	}

	q := fmt.Sprintf(
		"SELECT path, filename, file_mtime, date_taken, size, width, height FROM files %s %s %s",
		where, orderClause, limitClause,
	)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var images []ImageInfo
	for rows.Next() {
		var imgPath, filename string
		var mtimeNs, dateTakenNs, size int64
		var w, h int
		if err := rows.Scan(&imgPath, &filename, &mtimeNs, &dateTakenNs, &size, &w, &h); err != nil {
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
		})
	}
	return images, total, rows.Err()
}
