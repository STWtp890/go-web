package application

// PostgreSQL integration test for the rebuild fence. A rebuild derives each row
// from a snapshot of the applied-event ledger taken when its statement starts; a
// recall that commits while the statement waits on the row lock is invisible to
// that snapshot. This test reproduces the interleaving deterministically with two
// connections and asserts the recall still wins - without the revision guard the
// rebuild writes its stale snapshot over the newer row and resurrects a recalled
// record, which is exactly the failure the fence exists to prevent.

import (
	"context"
	"testing"
	"time"

	qqsearchv1 "packages/gen/qqsearch/v1"

	"github.com/jackc/pgx/v5/pgxpool"
)

// waitForLockWait blocks until the connection is waiting on a lock, or fails the
// test. It proves the rebuild statement really reached the locked row before the
// recall commits, so the test exercises the intended interleaving rather than
// passing because the two statements happened to serialize.
func waitForLockWait(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pid uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waitEventType string
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(wait_event_type, '') FROM pg_stat_activity WHERE pid = $1`, pid,
		).Scan(&waitEventType); err != nil {
			t.Fatalf("read the rebuild backend state: %v", err)
		}
		if waitEventType == "Lock" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the rebuild statement never blocked on the recalled row (wait_event_type=%q); "+
				"the interleaving this test needs did not happen", waitEventType)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIntegrationRebuildCannotResurrectARecordRecalledWhileItRuns(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()
	messageID := nextRecordID(t, "msg")
	fileID := nextRecordID(t, "file")

	mustApply(t, service, messageEventFor(messageID, "rebuild fence "+token, 1, "fence-msg-1-"+messageID))
	mustApply(t, service, fileEventFor(fileID, "rebuild-fence-"+token+".pdf", 1, "fence-file-1-"+fileID))

	// Connection A holds an in-flight recall: the row is updated to the recalled
	// revision, but nothing is committed, so the rebuild's snapshot still sees
	// revision 1 only.
	recallConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the recall connection: %v", err)
	}
	t.Cleanup(recallConn.Release)
	recallTx, err := recallConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the recall transaction: %v", err)
	}
	defer func() { _ = recallTx.Rollback(ctx) }()
	recall := mustEvent(t, recallEventFor(RecordKindMessage, messageID, 2, "fence-msg-2-"+messageID))
	if err := service.recallMessage(ctx, recallTx, recall); err != nil {
		t.Fatalf("apply the in-flight recall: %v", err)
	}

	// Connection B runs the real rebuild statement. It must block on the row A
	// holds.
	rebuildConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the rebuild connection: %v", err)
	}
	t.Cleanup(rebuildConn.Release)
	rebuildTx, err := rebuildConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the rebuild transaction: %v", err)
	}
	defer func() { _ = rebuildTx.Rollback(ctx) }()
	rebuildDone := make(chan error, 1)
	go func() {
		_, execErr := rebuildTx.Exec(ctx, rebuildMessages)
		rebuildDone <- execErr
	}()
	waitForLockWait(t, ctx, pool, rebuildConn.Conn().PgConn().PID())

	// The recall commits while the rebuild is waiting.
	if err := recallTx.Commit(ctx); err != nil {
		t.Fatalf("commit the recall: %v", err)
	}
	select {
	case err := <-rebuildDone:
		if err != nil {
			t.Fatalf("the rebuild statement failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the rebuild statement did not finish after the recall committed")
	}
	if err := rebuildTx.Commit(ctx); err != nil {
		t.Fatalf("commit the rebuild: %v", err)
	}

	// The recall wins: the row is recalled at revision 2, and the record-state
	// lookup reports it that way inside its granted scope.
	var (
		status   string
		revision int64
	)
	if err := pool.QueryRow(ctx,
		`SELECT status, record_revision FROM qq_search.qq_messages WHERE record_id = $1`, messageID,
	).Scan(&status, &revision); err != nil {
		t.Fatalf("read the recalled row: %v", err)
	}
	if status != string(StatusRecalled) || revision != 2 {
		t.Fatalf("row after the concurrent rebuild = %s/%d, want recalled/2: the rebuild rewound a "+
			"recall that committed while it was running", status, revision)
	}
	state := lookupState(t, service, grantGroupScope(testGroupID), messageStateRequest(messageID, nil))
	if !state.GetExists() || state.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("record state after the concurrent rebuild = %+v, want exists with RECALLED", state)
	}

	// The file model has its own statement and the same fence.
	fileRecallConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the file recall connection: %v", err)
	}
	t.Cleanup(fileRecallConn.Release)
	fileRecallTx, err := fileRecallConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the file recall transaction: %v", err)
	}
	defer func() { _ = fileRecallTx.Rollback(ctx) }()
	fileRecall := mustEvent(t, recallEventFor(RecordKindFile, fileID, 2, "fence-file-2-"+fileID))
	if err := service.recallFile(ctx, fileRecallTx, fileRecall); err != nil {
		t.Fatalf("apply the in-flight file recall: %v", err)
	}
	fileRebuildConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the file rebuild connection: %v", err)
	}
	t.Cleanup(fileRebuildConn.Release)
	fileRebuildTx, err := fileRebuildConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the file rebuild transaction: %v", err)
	}
	defer func() { _ = fileRebuildTx.Rollback(ctx) }()
	fileRebuildDone := make(chan error, 1)
	go func() {
		_, execErr := fileRebuildTx.Exec(ctx, rebuildFiles)
		fileRebuildDone <- execErr
	}()
	waitForLockWait(t, ctx, pool, fileRebuildConn.Conn().PgConn().PID())
	if err := fileRecallTx.Commit(ctx); err != nil {
		t.Fatalf("commit the file recall: %v", err)
	}
	select {
	case err := <-fileRebuildDone:
		if err != nil {
			t.Fatalf("the file rebuild statement failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the file rebuild statement did not finish after the recall committed")
	}
	if err := fileRebuildTx.Commit(ctx); err != nil {
		t.Fatalf("commit the file rebuild: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status, record_revision FROM qq_search.qq_files WHERE record_id = $1`, fileID,
	).Scan(&status, &revision); err != nil {
		t.Fatalf("read the recalled file row: %v", err)
	}
	if status != string(StatusRecalled) || revision != 2 {
		t.Fatalf("file row after the concurrent rebuild = %s/%d, want recalled/2", status, revision)
	}
}
