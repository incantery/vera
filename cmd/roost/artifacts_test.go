package main

import (
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *artifactStore {
	t.Helper()
	return &artifactStore{dir: t.TempDir()}
}

func TestArtifactLifecycle(t *testing.T) {
	st := testStore(t)
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	a, err := st.create("root-1", "design prompt", "# Draft\n\nhello", now)
	if err != nil || a.ID == "" {
		t.Fatalf("create: %+v %v", a, err)
	}
	got, err := st.get("root-1", a.ID)
	if err != nil || got.Content != "# Draft\n\nhello" || got.Title != "design prompt" {
		t.Fatalf("get: %+v %v", got, err)
	}
	later := now.Add(time.Minute)
	upd, err := st.update("root-1", a.ID, "", "# Draft v2", later)
	if err != nil || upd.Content != "# Draft v2" || upd.Title != "design prompt" || !upd.UpdatedAt.Equal(later) {
		t.Fatalf("update kept: %+v %v", upd, err)
	}
	list := st.list("root-1")
	if len(list) != 1 || list[0].Title != "design prompt" || list[0].Bytes != len("# Draft v2") {
		t.Fatalf("list: %+v", list)
	}
	if st.list("root-2") != nil {
		t.Fatal("another agent's shelf must be empty")
	}
	if err := st.delete("root-1", a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.get("root-1", a.ID); err == nil {
		t.Fatal("deleted and still readable")
	}
}

func TestArtifactIDsAreFilenameShapedOrRefused(t *testing.T) {
	st := testStore(t)
	for _, bad := range []string{"../escape", "a/b", "", ".hidden", "id with space"} {
		if _, err := st.get(bad, "x"); err == nil {
			t.Fatalf("root %q accepted", bad)
		}
		if _, err := st.get("root", bad); err == nil {
			t.Fatalf("id %q accepted", bad)
		}
	}
}

func TestArtifactUntitledGetsAName(t *testing.T) {
	st := testStore(t)
	a, err := st.create("root-1", "   ", "content", time.Now())
	if err != nil || a.Title != "untitled" {
		t.Fatalf("a=%+v err=%v", a, err)
	}
}

func TestArtifactListNewestFirst(t *testing.T) {
	st := testStore(t)
	now := time.Now()
	st.create("r", "old", "x", now.Add(-time.Hour))
	st.create("r", "new", "y", now)
	list := st.list("r")
	if len(list) != 2 || list[0].Title != "new" {
		titles := make([]string, len(list))
		for i, m := range list {
			titles[i] = m.Title
		}
		t.Fatalf("order: %s", strings.Join(titles, ","))
	}
}
