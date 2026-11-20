package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// BackupKeep is how many daily backups are kept.
const BackupKeep = 30

var backupName = regexp.MustCompile(`^finance-\d{4}-\d{2}-\d{2}\.db$`)

// Backup writes a consistent snapshot of the database to
// dir/finance-YYYY-MM-DD.db (local date) with VACUUM INTO, replacing a backup from
// the same day, then deletes all but the newest keep backups. The snapshot is
// written to a temporary name and renamed into place, so a crash never leaves a
// half-written file under a backup name. It returns the backup's path.
//
// These backups live on the same SD card as the database: they protect against
// mistakes and corruption, not against losing the card.
func (s *Store) Backup(ctx context.Context, dir string, keep int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(dir, "finance-"+s.Now().In(time.Local).Format("2006-01-02")+".db")
	tmp := final + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("backup: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return final, pruneBackups(dir, keep)
}

// pruneBackups deletes all but the newest keep backups (by the date in the name)
// and any leftover temporary files. Other files in dir are left alone.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var backups []string
	for _, e := range entries {
		switch name := e.Name(); {
		case e.IsDir():
		case backupName.MatchString(name):
			backups = append(backups, name)
		case strings.HasPrefix(name, "finance-") && strings.HasSuffix(name, ".db.tmp"):
			os.Remove(filepath.Join(dir, name))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(backups))) // ISO dates sort chronologically
	for _, name := range backups[min(keep, len(backups)):] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// LatestBackup returns the time the newest backup in dir was written, or the zero
// time if there is none.
func LatestBackup(dir string) (time.Time, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for _, e := range entries {
		if !backupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest, nil
}

// NextBackup is the next time of day hour:min (local time) strictly after now.
func NextBackup(now time.Time, hour, min int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, min, 0, 0, now.Location())
	}
	return next
}
