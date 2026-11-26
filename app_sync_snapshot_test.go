package main

import (
	"testing"

	"outline-reader/outline"
)

func TestSyncSnapshotComplete(t *testing.T) {
	live := map[string]docPlacement{
		"doc-1": {colID: "col-1"},
		"doc-2": {colID: "col-1"},
	}
	complete := map[string]outline.Document{
		"doc-1": {ID: "doc-1"},
		"doc-2": {ID: "doc-2"},
	}
	if !syncSnapshotComplete(true, live, complete, 2) {
		t.Fatal("complete full snapshot should allow deletion")
	}
	if syncSnapshotComplete(false, live, complete, 2) {
		t.Fatal("incremental snapshot must not allow deletion")
	}
	if syncSnapshotComplete(true, live, map[string]outline.Document{"doc-1": {ID: "doc-1"}}, 2) {
		t.Fatal("snapshot missing a live document must not allow deletion")
	}
	if syncSnapshotComplete(true, nil, nil, 2) {
		t.Fatal("empty snapshot must not delete an existing cache")
	}
}
