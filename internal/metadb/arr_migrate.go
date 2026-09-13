package metadb

import (
	"database/sql"
	"fmt"
)

// migrateARRMedia adds the raw_title column to arr_media if it does not exist.
// Uses PRAGMA table_info for reliable column detection instead of text parsing.
func (d *DB) migrateARRMedia() error {
	// Check whether raw_title already exists using PRAGMA table_info.
	rows, err := d.db.Query("PRAGMA table_info(arr_media)")
	if err != nil {
		return fmt.Errorf("migrate arr_media: PRAGMA table_info: %w", err)
	}

	hasRawTitle := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("migrate arr_media: scan pragma: %w", err)
		}
		if name == "raw_title" {
			hasRawTitle = true
			break
		}
	}
	rows.Close()

	if hasRawTitle {
		return nil
	}

	// Add the column only when it truly does not exist.
	if _, err := d.db.Exec(`ALTER TABLE arr_media ADD COLUMN raw_title TEXT DEFAULT ''`); err != nil {
		return fmt.Errorf("migrate arr_media: ALTER TABLE: %w", err)
	}

	if d.logger != nil {
		d.logger.Printf("[ARR Schema] Added raw_title column to arr_media")
	}
	return nil
}
