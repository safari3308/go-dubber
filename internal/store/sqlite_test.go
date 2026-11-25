package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStore_QueueLogsAndNotifications(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dubber.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// 1. Queue roundtrip
	now := time.Now().UTC()
	rows := []QueueRow{
		{
			ID:                     "job-1",
			SourcePath:             "/media/ep1.mkv",
			FileName:               "ep1.mkv",
			Status:                 "pending",
			Stage:                  "pending",
			ConfigJSON:             `{"dub_languages":["vi"]}`,
			AvailableLanguagesJSON: `[]`,
			CreatedAt:              now,
			UpdatedAt:              now,
		},
		{
			ID:                     "job-2",
			SourcePath:             "/media/ep2.mkv",
			FileName:               "ep2.mkv",
			Status:                 "completed",
			Stage:                  "completed",
			Progress:               100,
			ConfigJSON:             `{"dub_languages":["en"]}`,
			AvailableLanguagesJSON: `[]`,
			CreatedAt:              now,
			UpdatedAt:              now,
		},
	}
	if err := db.ReplaceQueue(rows); err != nil {
		t.Fatalf("ReplaceQueue failed: %v", err)
	}

	loaded, err := db.LoadQueue()
	if err != nil {
		t.Fatalf("LoadQueue failed: %v", err)
	}
	if len(loaded) != 2 || loaded[0].ID != "job-1" || loaded[1].ID != "job-2" {
		t.Fatalf("unexpected loaded queue: %+v", loaded)
	}

	// 2. Logs roundtrip
	if _, err := db.InsertLog("info", "job-1", "Started ep1"); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	if _, err := db.InsertLog("warn", "job-1", "Subsync skipped"); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	logs, err := db.ListLogs(10)
	if err != nil || len(logs) != 2 || logs[0].Message != "Subsync skipped" {
		t.Fatalf("unexpected logs (expected newest first): %+v err=%v", logs, err)
	}

	// 3. Unread Notifications roundtrip
	n1, err := db.InsertNotification("success", "Done", "ep1 finished")
	if err != nil {
		t.Fatalf("InsertNotification failed: %v", err)
	}
	if _, err := db.InsertNotification("error", "Failed", "ep2 failed"); err != nil {
		t.Fatalf("InsertNotification failed: %v", err)
	}

	unread, err := db.ListUnreadNotifications()
	if err != nil || len(unread) != 2 {
		t.Fatalf("expected 2 unread notifications, got %d err=%v", len(unread), err)
	}

	if err := db.MarkNotificationsRead(n1.ID); err != nil {
		t.Fatalf("MarkNotificationsRead failed: %v", err)
	}
	unreadAfter, _ := db.ListUnreadNotifications()
	if len(unreadAfter) != 1 || unreadAfter[0].Title != "Failed" {
		t.Fatalf("expected 1 unread notification remaining, got %+v", unreadAfter)
	}

	// 4. Settings roundtrip
	if _, found, err := db.LoadSettingsJSON(); err != nil || found {
		t.Fatalf("expected no settings initially, got found=%v err=%v", found, err)
	}
	if err := db.SaveSettingsJSON(`{"api_url":"http://localhost:8000"}`); err != nil {
		t.Fatalf("SaveSettingsJSON failed: %v", err)
	}
	gotCfg, found, err := db.LoadSettingsJSON()
	if err != nil || !found || gotCfg != `{"api_url":"http://localhost:8000"}` {
		t.Fatalf("unexpected loaded settings: found=%v val=%q err=%v", found, gotCfg, err)
	}
}
