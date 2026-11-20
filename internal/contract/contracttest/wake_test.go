// SPDX-License-Identifier: MIT

package contracttest_test

import (
	"testing"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

// A wake payload passes only in the shape the runner contract gives its
// reason, so a test of any module that wakes agents catches a payload the
// runner would refuse.
func TestValidateWakePayload(t *testing.T) {
	t.Parallel()
	chat := `{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"human:alice","text":"Hello","sentAt":"2026-11-19T19:00:01Z"}`
	tests := []struct {
		name    string
		reason  string
		payload string
		ok      bool
	}{
		{"chat message", "chat", chat, true},
		{"routed chat message", "chat", `{"id":"msg_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","chatId":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","from":"agent:router","text":"Hello","sentAt":"2026-11-19T19:00:01Z","routedBy":"router"}`, true},
		{"chat with a field the runner does not know", "chat", `{"message_id":"cht_01JD8X3Q5Y6Z7A8B9C0D1E2F40","text":"Hello","author":"human:alice"}`, false},
		{"chat payload under another reason", "message", chat, false},
		{"unknown reason", "dream", chat, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := contracttest.ValidateWakePayload(tt.reason, []byte(tt.payload))
			if (err == nil) != tt.ok {
				t.Fatalf("ValidateWakePayload(%s) = %v, want ok %v", tt.reason, err, tt.ok)
			}
		})
	}
}
