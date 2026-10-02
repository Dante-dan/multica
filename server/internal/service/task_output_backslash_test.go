package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// GH #9014: Output is already decoded JSON text, not an escaped CLI flag.
const completionMarkdown = `Math: $n \neq -1$, $n \to \infty$, $\text{for } i = 1,\dots,n$.
$$
\begin{aligned}
f(x) &= x^n \\
&\to \infty
\end{aligned}
$$
Also preserve \right, \theta, a literal \n, and C:\new\folder.`

func TestWriteChatCompletionOutcomePreservesBackslashes(t *testing.T) {
	f, _, _, agent := conditionFixture(t)
	session := f.ChatSession(t, agent)
	f.Cleanup(t, "DELETE FROM chat_message WHERE chat_session_id=$1", session)
	task := f.Task(t, agent, testutil.Cols{"chat_session_id": session})
	result, err := json.Marshal(protocol.TaskCompletedPayload{Output: completionMarkdown})
	if err != nil {
		t.Fatal(err)
	}
	row, err := f.svc.TaskSvc.writeChatCompletionOutcome(context.Background(), f.q, db.AgentTaskQueue{
		ID: util.MustParseUUID(task), ChatSessionID: util.MustParseUUID(session), AgentID: util.MustParseUUID(agent),
	}, result)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("no chat message written")
	}
	if row.Content != completionMarkdown {
		t.Fatalf("chat output changed:\n got: %q\nwant: %q", row.Content, completionMarkdown)
	}
}

func TestCompleteTaskFallbackCommentPreservesBackslashes(t *testing.T) {
	f, s, issue, agent := conditionFixture(t)
	f.Cleanup(t, "DELETE FROM comment WHERE issue_id=$1", issue)
	task := f.Task(t, agent, testutil.Cols{"issue_id": issue, "status": "running", "started_at": testutil.Raw("now()"),
		"runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + agent + "')")})
	result, err := json.Marshal(protocol.TaskCompletedPayload{Output: completionMarkdown})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tasks.CompleteTask(context.Background(), util.MustParseUUID(task), result, "", "", "", false, "", ""); err != nil {
		t.Fatal(err)
	}
	var body string
	f.QueryRow(t, "SELECT body FROM comment WHERE issue_id=$1 AND source_task_id=$2", issue, util.MustParseUUID(task)).Scan(&body)
	if body != completionMarkdown {
		t.Fatalf("comment output changed:\n got: %q\nwant: %q", body, completionMarkdown)
	}
}
