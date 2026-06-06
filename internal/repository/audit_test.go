package repository

import (
	"strings"
	"testing"
)

func TestUserAuditWhereIncludesUserOperatorAndPeerEvents(t *testing.T) {
	where, args := userAuditWhere(4242421234, "")

	checks := []string{
		"operator = ?",
		"action LIKE 'peer.%' AND detail->>'asn' = ?",
		"target_id IN (SELECT id::text FROM peers WHERE remote_asn = ?)",
		"action = 'admin.login_as' AND detail->>'asn' = ?",
	}
	for _, check := range checks {
		if !strings.Contains(where, check) {
			t.Fatalf("where clause missing %q: %s", check, where)
		}
	}

	wantArgs := []interface{}{"AS4242421234", "4242421234", int64(4242421234), "4242421234"}
	if len(args) != len(wantArgs) {
		t.Fatalf("len(args) = %d, want %d", len(args), len(wantArgs))
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %#v, want %#v", i, args[i], wantArgs[i])
		}
	}
}

func TestUserAuditWhereAppliesActionFilter(t *testing.T) {
	where, args := userAuditWhere(4242421234, "peer.approve")

	if !strings.Contains(where, "AND action = ?") {
		t.Fatalf("where clause missing action filter: %s", where)
	}
	if len(args) != 5 {
		t.Fatalf("len(args) = %d, want 5", len(args))
	}
	if args[4] != "peer.approve" {
		t.Fatalf("action arg = %#v, want peer.approve", args[4])
	}
}
