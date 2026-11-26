package main

import (
	"testing"
	"strconv"

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

func TestFlattenCollectionTreeLimitsAndOrder(t *testing.T) {
	nodes := []outline.NavNode{{ID: "root", Children: []outline.NavNode{{ID: "child"}}}}
	placements, next, err := flattenCollectionTree(nodes, "col", 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if next != 12 || placements["root"].pos != 11 || placements["child"].pos != 12 || placements["child"].parent != "root" {
		t.Fatalf("unexpected preorder placements: %#v next=%d", placements, next)
	}
	deep := outline.NavNode{ID: "n"}
	current := &deep
	for i := 1; i < maxCollectionTreeDepth+1; i++ {
		current.Children = []outline.NavNode{{ID: "n" + strconv.Itoa(i+1)}}
		current = &current.Children[0]
	}
	if _, _, err := flattenCollectionTree([]outline.NavNode{deep}, "col", 0, maxCollectionTreeNodes); err == nil {
		t.Fatal("overly deep tree should be rejected")
	}
	if _, _, err := flattenCollectionTree(nodes, "col", 0, 1); err == nil {
		t.Fatal("tree exceeding node limit should be rejected")
	}
}
