package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(filepath.Join(t.TempDir(), "test.db"), strings.Repeat("ab", 32))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSetSyncStatePreservesLastFull(t *testing.T) {
	db := newTestDB(t)
	full := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.SetLastFullSync(full))
	require.NoError(t, db.SetSyncState(full.Add(time.Minute), 1, 2, 3))

	assert.WithinDuration(t, full, db.GetLastFullSync(), 2*time.Second)
	st := db.GetSyncState()
	assert.Equal(t, 1, st.Added)
	assert.Equal(t, 2, st.Updated)
	assert.Equal(t, 3, st.Deleted)
	assert.False(t, db.GetLastSyncTime().IsZero())
	assert.True(t, time.Since(db.GetLastFullSync()) < time.Hour)
}

func TestSetLastFullSyncPreservesCounters(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.SetSyncState(now, 5, 6, 7))
	require.NoError(t, db.SetLastFullSync(now))

	st := db.GetSyncState()
	assert.Equal(t, 5, st.Added)
	assert.Equal(t, 6, st.Updated)
	assert.Equal(t, 7, st.Deleted)
	assert.WithinDuration(t, now, st.LastSync, 2*time.Second)
	assert.WithinDuration(t, now, db.GetLastFullSync(), 2*time.Second)
}

func TestSearchLikeFallbackEscapesWildcards(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.ApplySync(nil, []Document{
		{OutlineID: "a", Title: "Discount 50% today", Text: "alpha", Position: 1},
		{OutlineID: "b", Title: "Plain note", Text: "beta", Position: 2},
	}, nil, nil))
	db.fts = false

	hits, err := db.SearchDocuments("50%")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "a", hits[0].OutlineID)

	hits, err = db.SearchDocuments("%")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "a", hits[0].OutlineID)

	hits, err = db.SearchDocuments("_")
	require.NoError(t, err)
	assert.Empty(t, hits)

	hits, err = db.SearchDocuments("BETA")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "b", hits[0].OutlineID)
}

func TestSearchFTSWithoutMatchesDoesNotScanTable(t *testing.T) {
	db := newTestDB(t)
	require.True(t, db.fts)
	require.NoError(t, db.ApplySync(nil, []Document{
		{OutlineID: "a", Title: "Doc", Text: "abcdef", Position: 1},
	}, nil, nil))

	hits, err := db.SearchDocuments("bcd")
	require.NoError(t, err)
	assert.Empty(t, hits)

	hits, err = db.SearchDocuments("abc")
	require.NoError(t, err)
	assert.Len(t, hits, 1)
}

func TestDeleteBlobsNotInLargeSet(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SaveBlob(&Blob{ID: "keep", Mime: "image/png", Data: []byte{1}, Size: 1}))
	require.NoError(t, db.SaveBlob(&Blob{ID: "drop", Mime: "image/png", Data: []byte{2}, Size: 1}))

	ids := make([]string, 0, 40001)
	ids = append(ids, "keep")
	for i := 0; i < 40000; i++ {
		ids = append(ids, fmt.Sprintf("att-%d", i))
	}
	require.NoError(t, db.DeleteBlobsNotIn(ids))

	left, err := db.BlobIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"keep"}, left)
}

func TestApplySyncLargeDeleteList(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.ApplySync([]Collection{{OutlineID: "c1", Name: "Wiki"}}, []Document{
		{OutlineID: "d1", CollectionID: "c1", Title: "Doc", Text: "text", Position: 1},
	}, nil, nil))

	ids := make([]string, 0, 40001)
	for i := 0; i < 40000; i++ {
		ids = append(ids, fmt.Sprintf("gone-%d", i))
	}
	ids = append(ids, "d1")
	require.NoError(t, db.ApplySync([]Collection{{OutlineID: "c1", Name: "Wiki"}}, nil, nil, ids))
	assert.Equal(t, int64(0), db.CountDocuments())

	cols, err := db.GetCollections()
	require.NoError(t, err)
	assert.Len(t, cols, 1)
}

func TestPrefsRoundTrip(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SetPrefs(map[string]string{"theme": "light", "sideWidth": "320"}))
	require.NoError(t, db.SetPrefs(map[string]string{"theme": "dark"}))

	p, err := db.GetPrefs()
	require.NoError(t, err)
	assert.Equal(t, "dark", p["theme"])
	assert.Equal(t, "320", p["sideWidth"])
}
