package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A retry inherits the rule's original revision, so only a still-claimable
// rule may create it. A naturally-ended rule is distinct from a disabled one.
func TestIssueWakeupRetryRequiresClaimableRule(t *testing.T) {
	for _, change := range []string{"unchanged", "ended", "disabled", "edited", "deleted"} {
		t.Run(change, func(t *testing.T) {
			f, s, issue, agent := wakeFixture(t)
			ctx := context.Background()
			w := wakeCreate(t, f, s, issue, WakeupInput{AgentID: agent, Kind: "event", EventTypes: []string{"comment.created"}, Instruction: "check"})
			f.Comment(t, util.UUIDToString(issue), "wake")
			wakeDispatch(t, s, w)
			var parent pgtype.UUID
			if err := f.Pool.QueryRow(ctx, "UPDATE agent_task_queue SET status='failed',failure_reason='runtime_offline',attempt=1,max_attempts=3 WHERE context->>'wakeup_id'=$1 RETURNING id", util.UUIDToString(w.ID)).Scan(&parent); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "ended":
				f.Exec(t, "UPDATE issue_wakeup SET enabled=false WHERE id=$1", w.ID)
			case "disabled":
				f.Exec(t, "UPDATE issue_wakeup SET enabled=false,disabled_at=now() WHERE id=$1", w.ID)
			case "edited":
				f.Exec(t, "UPDATE issue_wakeup SET revision=revision+1 WHERE id=$1", w.ID)
			case "deleted":
				f.Exec(t, "DELETE FROM issue_wakeup WHERE id=$1", w.ID)
			}
			child, err := f.q.CreateRetryTask(ctx, db.CreateRetryTaskParams{ID: parent})
			if change == "unchanged" || change == "ended" {
				if err != nil || child.Status != "queued" {
					t.Fatalf("valid retry = %s, %v", child.Status, err)
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("invalid retry = %s, %v; want no row", child.Status, err)
			}
		})
	}
}

// Old rows and a concurrent rule edit can bypass the insert-time snapshot.
// The sweeper must retire these rows without an age or runtime-health gate.
func TestIssueWakeupInvalidRetryBackstop(t *testing.T) {
	for _, status := range []string{"queued", "deferred"} {
		t.Run(status, func(t *testing.T) {
			f, s, issue, agent := wakeFixture(t)
			ctx := context.Background()
			w := wakeCreate(t, f, s, issue, WakeupInput{AgentID: agent, Kind: "event", EventTypes: []string{"comment.created"}, Instruction: "check"})
			f.Comment(t, util.UUIDToString(issue), "wake")
			wakeDispatch(t, s, w)
			f.Exec(t, "UPDATE agent_task_queue SET status=$2,fire_at=now()+interval '1 hour' WHERE context->>'wakeup_id'=$1", util.UUIDToString(w.ID), status)
			args := db.ExpireStaleQueuedTasksParams{ReconnectGraceSecs: 86400, MaxPerTick: 100}
			rows, err := s.Tasks.ExpireStaleQueuedTasks(ctx, args)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.IssueID == issue {
					t.Fatal("valid fresh wakeup expired")
				}
			}
			f.Exec(t, "UPDATE issue_wakeup SET revision=revision+1 WHERE id=$1", w.ID)
			rows, err = s.Tasks.ExpireStaleQueuedTasks(ctx, args)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.IssueID == issue {
					found = true
					if row.Status != "failed" || !row.CompletedAt.Valid || row.FailureReason.String != "queued_expired" {
						t.Fatalf("invalid terminal row: %+v", row)
					}
				}
			}
			if !found {
				t.Fatal("invalid fresh wakeup did not expire")
			}
		})
	}
}
