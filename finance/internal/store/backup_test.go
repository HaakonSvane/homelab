package store

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestBackupIsAUsableSnapshot(t *testing.T) {
	s := newStore(t)
	if err := s.Seed(ctx, 2026); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	path, err := s.Backup(ctx, dir, BackupKeep)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "finance-"+testNow.In(time.Local).Format("2006-01-02")+".db" {
		t.Errorf("backup name = %s", path)
	}

	// The backup opens as a database with the same data and settles the same way.
	// (Open a copy: opening a database in WAL mode creates -wal/-shm files beside it.)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, data, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	orig, _ := s.LedgerInput(ctx)
	copied, err := b.LedgerInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := month.MustParse("2026-11")
	so, _ := ledger.Settle(orig, m)
	sc, err := ledger.Settle(copied, m)
	if err != nil || sc.Felleskonto != so.Felleskonto || sc.Felleskonto != money.Kr(29_974) {
		t.Errorf("settlement from backup = %v (%v), original %v", sc.Felleskonto, err, so.Felleskonto)
	}

	// A second backup the same day replaces the first and leaves no temp file.
	if _, err := s.Backup(ctx, dir, BackupKeep); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, dir); len(got) != 1 {
		t.Errorf("files after two backups the same day: %v", got)
	}
	if latest, err := LatestBackup(dir); err != nil || latest.IsZero() {
		t.Errorf("LatestBackup = %v, %v", latest, err)
	}
}

func TestBackupRetention(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	// 32 days of backups, plus files that must survive (or be cleaned) untouched.
	day := time.Date(2026, 10, 1, 3, 30, 0, 0, time.Local)
	for i := 0; i < 32; i++ {
		d := day.AddDate(0, 0, i)
		s.Now = func() time.Time { return d }
		if _, err := s.Backup(ctx, dir, BackupKeep); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep me"), 0o644)
	os.WriteFile(filepath.Join(dir, "finance-2026-11-02.db.tmp"), []byte("half"), 0o644)
	s.Now = func() time.Time { return day.AddDate(0, 0, 32) }
	if _, err := s.Backup(ctx, dir, BackupKeep); err != nil {
		t.Fatal(err)
	}
	got := listDir(t, dir)
	if len(got) != BackupKeep+1 || got[0] != "finance-2026-10-04.db" || got[BackupKeep-1] != "finance-2026-11-02.db" || got[BackupKeep] != "notes.txt" {
		t.Errorf("after 33 days: %d files, first %s, last %s", len(got), got[0], got[len(got)-1])
	}
}

func TestBackupRefusesUnwritableDir(t *testing.T) {
	s := newStore(t)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	os.WriteFile(file, nil, 0o644)
	if _, err := s.Backup(ctx, file, BackupKeep); err == nil {
		t.Error("backup into a file path succeeded")
	}
}

func TestNextBackup(t *testing.T) {
	oslo, _ := time.LoadLocation("Europe/Oslo")
	for _, c := range []struct{ now, want time.Time }{
		{time.Date(2026, 11, 20, 1, 0, 0, 0, oslo), time.Date(2026, 11, 20, 3, 30, 0, 0, oslo)},
		{time.Date(2026, 11, 20, 3, 30, 0, 0, oslo), time.Date(2026, 11, 21, 3, 30, 0, 0, oslo)},
		{time.Date(2026, 11, 20, 22, 0, 0, 0, oslo), time.Date(2026, 11, 21, 3, 30, 0, 0, oslo)},
		{time.Date(2026, 12, 31, 23, 0, 0, 0, oslo), time.Date(2027, 1, 1, 3, 30, 0, 0, oslo)},
		// Across the switch to summer time (02:00 → 03:00 on 29 March 2026).
		{time.Date(2026, 3, 28, 12, 0, 0, 0, oslo), time.Date(2026, 3, 29, 3, 30, 0, 0, oslo)},
	} {
		if got := NextBackup(c.now, 3, 30); !got.Equal(c.want) {
			t.Errorf("NextBackup(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}
