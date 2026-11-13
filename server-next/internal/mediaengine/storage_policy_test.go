package mediaengine

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedKeepingChangesConversionOutputNotPreparedDownloads(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	docs, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{settings: docs}
	for _, policy := range []struct {
		word    string
		seconds int
	}{{"balanced", 300}, {"short", 60}, {"long", 1800}} {
		word, want := policy.word, policy.seconds
		raw, _ := json.Marshal(map[string]string{"keepGeneratedFiles": word})
		if _, err := docs.Write(ctx, 0, settings.Storage.Name(), raw, -1, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := e.Delivery().PlayedRetentionSeconds; got != want {
			t.Fatalf("%s: conversion retention=%d, want %d", word, got, want)
		}
		if got := prepare.Document.Get(docs).KeepDays; got != 30 {
			t.Fatalf("%s changed Prepared downloads to %d", word, got)
		}
	}
	for _, days := range []int{7, 14, 30, 90, 365} {
		raw, _ := json.Marshal(map[string]int{"keepDays": days})
		if _, err := docs.Write(ctx, 0, prepare.Document.Name(), raw, -1, time.Now()); err != nil {
			t.Fatal(days, err)
		}
		if got := e.Delivery().PlayedRetentionSeconds; got != 1800 {
			t.Fatalf("download keeping changed conversion retention: %d", got)
		}
	}
}
