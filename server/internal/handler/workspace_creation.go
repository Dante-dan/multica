package handler

import "strings"

func workspaceCreationAllowed(email string, disabled bool, emails, domains []string) bool {
	if disabled {
		return false
	}
	if len(emails) == 0 && len(domains) == 0 {
		return true
	}
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	for _, allowed := range emails {
		if strings.EqualFold(strings.TrimSpace(allowed), email) {
			return true
		}
	}
	for _, domain := range domains {
		if strings.EqualFold(strings.TrimSpace(domain), email[at+1:]) {
			return true
		}
	}
	return false
}
