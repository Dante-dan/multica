package handler

import "testing"

type creationPolicyTestConfig struct {
	DisableWorkspaceCreation                                             bool
	WorkspaceCreationAllowedEmails, WorkspaceCreationAllowedEmailDomains []string
}

func TestWorkspaceCreationPolicy(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cfg   creationPolicyTestConfig
		email string
		want  bool
	}{
		{name: "default", email: "any@example.com", want: true},
		{name: "global denial overrides email", cfg: creationPolicyTestConfig{DisableWorkspaceCreation: true, WorkspaceCreationAllowedEmails: []string{"lead@example.com"}}, email: "lead@example.com"},
		{name: "email", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmails: []string{" LEAD@example.com "}}, email: "lead@EXAMPLE.com", want: true},
		{name: "unlisted", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmails: []string{"lead@example.com"}}, email: "member@example.com"},
		{name: "domain", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmailDomains: []string{" EXAMPLE.com "}}, email: "lead@example.COM", want: true},
		{name: "subdomain denied", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmailDomains: []string{"example.com"}}, email: "lead@sub.example.com"},
		{name: "suffix denied", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmailDomains: []string{"example.com"}}, email: "lead@otherexample.com"},
		{name: "email or domain", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmails: []string{"lead@other.com"}, WorkspaceCreationAllowedEmailDomains: []string{"example.com"}}, email: "lead@other.com", want: true},
		{name: "domain or email", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmails: []string{"lead@other.com"}, WorkspaceCreationAllowedEmailDomains: []string{"example.com"}}, email: "member@example.com", want: true},
		{name: "empty email", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmailDomains: []string{"example.com"}}},
		{name: "malformed email", cfg: creationPolicyTestConfig{WorkspaceCreationAllowedEmailDomains: []string{"example.com"}}, email: "@example.com"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspaceCreationAllowed(tt.email, tt.cfg.DisableWorkspaceCreation, tt.cfg.WorkspaceCreationAllowedEmails, tt.cfg.WorkspaceCreationAllowedEmailDomains); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
