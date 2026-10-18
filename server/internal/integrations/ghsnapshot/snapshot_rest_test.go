package ghsnapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the actual denied query and authenticated fallback together. These
// API fixtures never contact GitHub or require a Cloud installation.
func TestFetchPRSnapshotPermissionFallback(t *testing.T) {
	for _, scenario := range []string{"passed", "failed", "null_conclusion", "paginated", "empty", "head_changed", "wrong_check_head", "too_many_suites", "rate_limited", "unrelated_error", "status_error", "page_limit"} {
		t.Run(scenario, func(t *testing.T) {
			metadataCalls, restCalls := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "" {
					t.Error("missing authorization")
				}
				if strings.HasSuffix(r.URL.Path, "/access_tokens") {
					w.WriteHeader(201)
					fmt.Fprint(w, `{"token":"secret"}`)
					return
				}
				if r.URL.Path == "/graphql" {
					var body struct {
						Query string `json:"query"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					if strings.Contains(body.Query, "statusCheckRollup") {
						typ := "FORBIDDEN"
						if scenario == "unrelated_error" {
							typ = "NOT_FOUND"
						}
						fmt.Fprintf(w, `{"errors":[{"type":%q,"message":"denied"}]}`, typ)
						return
					}
					metadataCalls++
					head := "sha1"
					if metadataCalls > 1 && scenario == "head_changed" {
						head = "sha2"
					}
					fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"headRefOid":%q,"mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED"}}}}`, head)
					return
				}
				restCalls++
				if !strings.HasPrefix(r.URL.Path, "/repos/o/r/commits/sha1/") {
					t.Errorf("unpinned path %s", r.URL.Path)
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/check-suites"):
					count := 2
					if scenario == "too_many_suites" {
						count = 1001
					}
					fmt.Fprintf(w, `{"total_count":%d}`, count)
				case strings.HasSuffix(r.URL.Path, "/check-runs"):
					if r.URL.Query().Get("filter") != "latest" {
						t.Error("missing latest filter")
					}
					if scenario == "rate_limited" {
						w.Header().Set("Retry-After", "10")
						w.WriteHeader(403)
						return
					}
					if scenario == "empty" {
						fmt.Fprint(w, `{"check_runs":[]}`)
						return
					}
					head := "sha1"
					if scenario == "wrong_check_head" {
						head = "sha2"
					}
					if r.URL.Query().Get("page") == "1" || scenario == "page_limit" {
						w.Header().Set("Link", `<https://api.github.com/next>; rel="next"`)
						conclusion := "neutral"
						if scenario == "failed" {
							conclusion = "failure"
						}
						if scenario == "null_conclusion" {
							conclusion = ""
						}
						fmt.Fprintf(w, `{"check_runs":[{"name":"build","head_sha":%q,"status":"completed","conclusion":%q,"details_url":"u1"}]}`, head, conclusion)
					} else {
						if scenario == "passed" || scenario == "failed" || scenario == "null_conclusion" {
							fmt.Fprint(w, `{"check_runs":[{"name":"test","head_sha":"sha1","status":"completed","conclusion":"success"}]}`)
						} else {
							fmt.Fprint(w, `{"check_runs":[{"name":"test","head_sha":"sha1","status":"waiting","conclusion":null}]}`)
						}
					}
				case strings.HasSuffix(r.URL.Path, "/statuses"):
					if scenario == "status_error" {
						w.WriteHeader(403)
						return
					}
					if scenario == "empty" {
						fmt.Fprint(w, `[]`)
						return
					}
					if r.URL.Query().Get("page") == "1" {
						w.Header().Set("Link", `<https://api.github.com/next>; rel="next"`)
						fmt.Fprint(w, `[{"context":"deploy","state":"success","target_url":"u2"}]`)
					} else {
						state := "pending"
						if scenario == "passed" || scenario == "failed" || scenario == "null_conclusion" {
							state = "success"
						}
						fmt.Fprintf(w, `[{"context":"deploy","state":"failure"},{"context":"lint","state":%q}]`, state)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			snap, err := FetchPRSnapshot(context.Background(), newTestClient(t, srv.URL), 1, "o", "r", 5)
			switch scenario {
			case "paginated":
				if err != nil {
					t.Fatal(err)
				}
				if snap.HeadSHA != "sha1" || snap.MergeStateStatus != "BLOCKED" || snap.Mergeable != "MERGEABLE" || !snap.HasChecks || snap.RollupState != "PENDING" || len(snap.Contexts) != 4 {
					t.Fatalf("unexpected snapshot: %+v", snap)
				}
				if snap.Contexts[0].Conclusion != "neutral" || snap.Contexts[1].Status != "queued" || snap.Contexts[2].Conclusion != "success" || !snap.Contexts[2].IsStatusContext || snap.Contexts[2].DetailsURL != "u2" {
					t.Fatalf("normalization: %+v", snap.Contexts)
				}
				if metadataCalls != 2 || snap.Decided() {
					t.Fatalf("metadata calls=%d snapshot=%+v", metadataCalls, snap)
				}
			case "passed", "failed", "null_conclusion":
				want := "SUCCESS"
				if scenario == "failed" {
					want = "FAILURE"
				}
				if scenario == "null_conclusion" {
					want = "PENDING"
				}
				if err != nil || snap == nil || snap.RollupState != want {
					t.Fatalf("snapshot=%+v err=%v want rollup=%s", snap, err, want)
				}
			case "empty":
				if err != nil || snap.HasChecks || snap.RollupState != "" || len(snap.Contexts) != 0 {
					t.Fatalf("empty snapshot=%+v err=%v", snap, err)
				}
			default:
				if err == nil || snap != nil {
					t.Fatalf("expected atomic failure, snapshot=%+v err=%v", snap, err)
				}
				if scenario == "unrelated_error" && (restCalls != 0 || metadataCalls != 0) {
					t.Fatal("unrelated query error triggered fallback")
				}
				if scenario == "rate_limited" {
					if _, ok := err.(*RateLimitError); !ok {
						t.Fatalf("rate limit type lost: %T", err)
					}
				}
			}
		})
	}
}
