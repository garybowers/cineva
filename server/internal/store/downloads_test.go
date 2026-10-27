package store

import (
	"context"
	"slices"
	"testing"
)

func TestDownloadJobs(t *testing.T) {
	st := newTestStore(t)
	f := newBrowseFixture(t, st)
	ctx := context.Background()
	tracks := []Item{f.tracks["Arabella"], f.tracks["R U Mine?"]}

	jobs, err := st.CreateDownloadJobs(ctx, f.user.ID, "dev-1", "opus-128", tracks, func(Item) (string, int64) { return DownloadQueued, 0 })
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ItemID != tracks[0].ID || jobs[0].Status != DownloadQueued || jobs[0].DeviceID != "dev-1" {
		t.Fatalf("jobs %+v", jobs)
	}
	if n, _ := st.UnfinishedDownloadJobs(ctx, f.user.ID); n != 2 {
		t.Errorf("unfinished %d", n)
	}
	if err := st.SetDownloadJobStatus(ctx, jobs[0].ID, DownloadReady, 1234, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDownloadJobStatus(ctx, jobs[1].ID, DownloadFailed, 0, "the share went away"); err != nil {
		t.Fatal(err)
	}
	got, err := st.DownloadJob(ctx, jobs[0].ID)
	if err != nil || got.Status != DownloadReady || got.Bytes != 1234 {
		t.Errorf("ready job %+v %v", got, err)
	}
	if got, _ := st.DownloadJob(ctx, jobs[1].ID); got.Status != DownloadFailed || got.Error != "the share went away" {
		t.Errorf("failed job %+v", got)
	}
	if n, _ := st.UnfinishedDownloadJobs(ctx, f.user.ID); n != 0 {
		t.Errorf("unfinished after %d", n)
	}
	list, err := st.DownloadJobs(ctx, f.user.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list %v %+v", err, list)
	}
	// Finished jobs older than the window go; fresh ones stay.
	if n, err := st.PruneDownloadJobs(ctx, 1); err != nil || n != 0 {
		t.Errorf("prune fresh: %d %v", n, err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE download_jobs SET updated_at = datetime('now', '-2 days') WHERE id = ?`, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PruneDownloadJobs(ctx, 1); err != nil || n != 1 {
		t.Errorf("prune old: %d %v", n, err)
	}
	if err := st.DeleteDownloadJob(ctx, "someone-else", jobs[1].ID); err == nil {
		t.Error("deleted another user's job")
	}
	if err := st.DeleteDownloadJob(ctx, f.user.ID, jobs[1].ID); err != nil {
		t.Error(err)
	}
	if _, err := st.DownloadJob(ctx, jobs[1].ID); err == nil {
		t.Error("deleted job still there")
	}

	ids, err := st.LibraryItemIDs(ctx, f.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3+4+8 || !slices.Contains(ids, f.am.ID) || !slices.IsSorted(ids) {
		t.Errorf("ids: %d", len(ids))
	}
}
