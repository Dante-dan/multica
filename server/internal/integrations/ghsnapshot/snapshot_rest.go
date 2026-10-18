package ghsnapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// PR metadata needs Pull requests:read, without traversing the commit object
// that requires Contents:read. Keep GitHub's mergeability enums unchanged.
const prMetadataQuery = `query($owner:String!,$repo:String!,$number:Int!){
 repository(owner:$owner,name:$repo){pullRequest(number:$number){
 headRefOid mergeable mergeStateStatus
 }}}
`

func fetchPRMetadata(ctx context.Context, c *Client, installationID int64, owner, repo string, number int32) (*graphqlPullRequest, error) {
	data, err := c.graphQL(ctx, installationID, prMetadataQuery, map[string]any{"owner": owner, "repo": repo, "number": number})
	if err != nil {
		return nil, err
	}
	var parsed graphqlPRData
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, errors.New("ghsnapshot: malformed pull request data")
	}
	pr := parsed.Repository.PullRequest
	if pr == nil || pr.HeadRefOid == "" {
		return nil, errors.New("ghsnapshot: pull request not found")
	}
	return pr, nil
}

// restPage deliberately does not follow an API-provided URL: credentials only
// go to the configured API host. Callers advance numbered pages at a pinned SHA.
func (c *Client) restPage(ctx context.Context, installationID int64, path string, dest any) (bool, error) {
	token, err := c.installationToken(ctx, installationID)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.apiBase, "/")+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && (resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0")) {
		return false, rateLimitFromResponse(resp, c.now())
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("github snapshot REST: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dest); err != nil {
		return false, errors.New("github snapshot REST: malformed response")
	}
	return strings.Contains(resp.Header.Get("Link"), `rel="next"`), nil
}

func fetchPRSnapshotREST(ctx context.Context, c *Client, installationID int64, owner, repo string, number int32) (*PRSnapshot, error) {
	pr, err := fetchPRMetadata(ctx, c, installationID, owner, repo, number)
	if err != nil {
		return nil, err
	}
	snap := &PRSnapshot{HeadSHA: pr.HeadRefOid, Mergeable: pr.Mergeable, MergeStateStatus: pr.MergeStateStatus}
	prefix := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/" + url.PathEscape(snap.HeadSHA)
	// GitHub caps the reference check-runs endpoint at 1,000 check suites.
	// Refuse that unsupported case rather than storing a truncated snapshot.
	var suites struct {
		TotalCount int `json:"total_count"`
	}
	if _, err := c.restPage(ctx, installationID, prefix+"/check-suites?per_page=1", &suites); err != nil {
		return nil, err
	}
	if suites.TotalCount > 1000 {
		return nil, errors.New("ghsnapshot: check suites exceed reference endpoint limit")
	}
	for page := 1; ; page++ {
		var runs struct {
			CheckRuns []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				DetailsURL string `json:"details_url"`
				HeadSHA    string `json:"head_sha"`
			} `json:"check_runs"`
		}
		next, err := c.restPage(ctx, installationID, fmt.Sprintf("%s/check-runs?filter=latest&per_page=100&page=%d", prefix, page), &runs)
		if err != nil {
			return nil, err
		}
		for _, run := range runs.CheckRuns {
			if run.HeadSHA != snap.HeadSHA {
				return nil, errors.New("ghsnapshot: check run head does not match pull request")
			}
			snap.Contexts = append(snap.Contexts, CheckContext{Name: run.Name, Status: normalizeRunStatus(run.Status), Conclusion: strings.ToLower(run.Conclusion), DetailsURL: run.DetailsURL})
		}
		if !next {
			break
		}
		if page >= maxSnapshotContextPages {
			return nil, errors.New("ghsnapshot: check-context pagination exceeds page limit")
		}
	}
	// Status history is newest first. Only the latest value of each context
	// belongs in the snapshot; a failed predecessor must not override a retry.
	seen := map[string]bool{}
	for page := 1; ; page++ {
		var statuses []struct {
			Context   string `json:"context"`
			State     string `json:"state"`
			TargetURL string `json:"target_url"`
		}
		next, err := c.restPage(ctx, installationID, fmt.Sprintf("%s/statuses?per_page=100&page=%d", prefix, page), &statuses)
		if err != nil {
			return nil, err
		}
		for _, s := range statuses {
			if seen[s.Context] {
				continue
			}
			seen[s.Context] = true
			status, conclusion := normalizeStatusState(s.State)
			snap.Contexts = append(snap.Contexts, CheckContext{Name: s.Context, Status: status, Conclusion: conclusion, DetailsURL: s.TargetURL, IsStatusContext: true})
		}
		if !next {
			break
		}
		if page >= maxSnapshotContextPages {
			return nil, errors.New("ghsnapshot: status pagination exceeds page limit")
		}
	}
	current, err := fetchPRMetadata(ctx, c, installationID, owner, repo, number)
	if err != nil {
		return nil, err
	}
	if current.HeadRefOid != snap.HeadSHA {
		return nil, errors.New("ghsnapshot: pull request head changed during pagination")
	}
	// The final metadata read is more recent, and still describes the same head.
	snap.Mergeable = current.Mergeable
	snap.MergeStateStatus = current.MergeStateStatus
	snap.HasChecks = len(snap.Contexts) > 0
	if snap.HasChecks {
		snap.RollupState = "SUCCESS"
		for _, check := range snap.Contexts {
			if check.Status != "completed" || check.Conclusion == "" {
				snap.RollupState = "PENDING"
				break
			}
		}
		// A failed context keeps the rollup failed even if others are pending.
		for _, check := range snap.Contexts {
			switch check.Conclusion {
			case "failure", "error", "cancelled", "timed_out", "action_required", "startup_failure", "stale":
				snap.RollupState = "FAILURE"
			}
		}
	}
	return snap, nil
}
