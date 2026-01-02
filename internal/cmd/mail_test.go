package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func TestMatchWorkerPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		caller  string
		want    bool
	}{
		// Exact matches
		{
			name:    "exact match",
			pattern: "gastown/polecats/capable",
			caller:  "gastown/polecats/capable",
			want:    true,
		},
		{
			name:    "exact match with different name",
			pattern: "gastown/polecats/toast",
			caller:  "gastown/polecats/capable",
			want:    false,
		},

		// Wildcard at end
		{
			name:    "wildcard matches polecat",
			pattern: "gastown/polecats/*",
			caller:  "gastown/polecats/capable",
			want:    true,
		},
		{
			name:    "wildcard matches different polecat",
			pattern: "gastown/polecats/*",
			caller:  "gastown/polecats/toast",
			want:    true,
		},
		{
			name:    "wildcard doesn't match wrong rig",
			pattern: "gastown/polecats/*",
			caller:  "beads/polecats/capable",
			want:    false,
		},
		{
			name:    "wildcard doesn't match nested path",
			pattern: "gastown/polecats/*",
			caller:  "gastown/polecats/sub/capable",
			want:    false,
		},

		// Crew patterns
		{
			name:    "crew wildcard matches",
			pattern: "gastown/crew/*",
			caller:  "gastown/crew/max",
			want:    true,
		},
		{
			name:    "crew wildcard doesn't match polecats",
			pattern: "gastown/crew/*",
			caller:  "gastown/polecats/capable",
			want:    false,
		},

		// Different rigs
		{
			name:    "different rig wildcard",
			pattern: "beads/polecats/*",
			caller:  "beads/polecats/capable",
			want:    true,
		},

		// Edge cases
		{
			name:    "empty pattern",
			pattern: "",
			caller:  "gastown/polecats/capable",
			want:    false,
		},
		{
			name:    "empty caller",
			pattern: "gastown/polecats/*",
			caller:  "",
			want:    false,
		},
		{
			name:    "pattern is just wildcard",
			pattern: "*",
			caller:  "anything",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchWorkerPattern(tt.pattern, tt.caller)
			if got != tt.want {
				t.Errorf("matchWorkerPattern(%q, %q) = %v, want %v",
					tt.pattern, tt.caller, got, tt.want)
			}
		})
	}
}

func TestIsEligibleWorker(t *testing.T) {
	tests := []struct {
		name     string
		caller   string
		patterns []string
		want     bool
	}{
		{
			name:     "matches first pattern",
			caller:   "gastown/polecats/capable",
			patterns: []string{"gastown/polecats/*", "gastown/crew/*"},
			want:     true,
		},
		{
			name:     "matches second pattern",
			caller:   "gastown/crew/max",
			patterns: []string{"gastown/polecats/*", "gastown/crew/*"},
			want:     true,
		},
		{
			name:     "matches none",
			caller:   "beads/polecats/capable",
			patterns: []string{"gastown/polecats/*", "gastown/crew/*"},
			want:     false,
		},
		{
			name:     "empty patterns list",
			caller:   "gastown/polecats/capable",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "nil patterns",
			caller:   "gastown/polecats/capable",
			patterns: nil,
			want:     false,
		},
		{
			name:     "exact match in list",
			caller:   "mayor/",
			patterns: []string{"mayor/", "gastown/witness"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEligibleWorker(tt.caller, tt.patterns)
			if got != tt.want {
				t.Errorf("isEligibleWorker(%q, %v) = %v, want %v",
					tt.caller, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestMailReleaseValidation tests the validation logic for the release command.
// This tests that release correctly identifies:
// - Messages not claimed (still in queue)
// - Messages claimed by a different worker
// - Messages without queue labels (non-queue messages)
func TestMailReleaseValidation(t *testing.T) {
	tests := []struct {
		name        string
		msgInfo     *messageInfo
		caller      string
		wantErr     bool
		errContains string
	}{
		{
			name: "caller matches assignee - valid release",
			msgInfo: &messageInfo{
				ID:        "hq-test1",
				Title:     "Test Message",
				Assignee:  "gastown/polecats/nux",
				QueueName: "work/gastown",
				Status:    "in_progress",
			},
			caller:  "gastown/polecats/nux",
			wantErr: false,
		},
		{
			name: "message still in queue - not claimed",
			msgInfo: &messageInfo{
				ID:        "hq-test2",
				Title:     "Test Message",
				Assignee:  "queue:work/gastown",
				QueueName: "work/gastown",
				Status:    "open",
			},
			caller:      "gastown/polecats/nux",
			wantErr:     true,
			errContains: "not claimed",
		},
		{
			name: "claimed by different worker",
			msgInfo: &messageInfo{
				ID:        "hq-test3",
				Title:     "Test Message",
				Assignee:  "gastown/polecats/other",
				QueueName: "work/gastown",
				Status:    "in_progress",
			},
			caller:      "gastown/polecats/nux",
			wantErr:     true,
			errContains: "was claimed by",
		},
		{
			name: "not a queue message",
			msgInfo: &messageInfo{
				ID:        "hq-test4",
				Title:     "Test Message",
				Assignee:  "gastown/polecats/nux",
				QueueName: "", // No queue label
				Status:    "open",
			},
			caller:      "gastown/polecats/nux",
			wantErr:     true,
			errContains: "not a queue message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRelease(tt.msgInfo, tt.caller)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// validateRelease checks if a message can be released by the caller.
// This is extracted for testing; the actual release command uses this logic inline.
func validateRelease(msgInfo *messageInfo, caller string) error {
	// Verify message is a queue message
	if msgInfo.QueueName == "" {
		return fmt.Errorf("message %s is not a queue message (no queue label)", msgInfo.ID)
	}

	// Verify caller is the one who claimed it
	if msgInfo.Assignee != caller {
		if strings.HasPrefix(msgInfo.Assignee, "queue:") {
			return fmt.Errorf("message %s is not claimed (still in queue)", msgInfo.ID)
		}
		return fmt.Errorf("message %s was claimed by %s, not %s", msgInfo.ID, msgInfo.Assignee, caller)
	}

	return nil
}
