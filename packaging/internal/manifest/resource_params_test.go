package manifest

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func TestInferResourceParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entryTS string
		want    []tooldef.ResourceParam
	}{
		{
			name:    "single resource list",
			entryTS: "tools/account.tickets.list.ts",
			want: []tooldef.ResourceParam{
				{Name: "account_id", BindingName: "account_id"},
			},
		},
		{
			name:    "single resource get includes deepest",
			entryTS: "tools/account.tickets.get.ts",
			want: []tooldef.ResourceParam{
				{Name: "account_id", BindingName: "account_id"},
				{Name: "ticket_id", BindingName: "ticket_id"},
			},
		},
		{
			name:    "nested resources list",
			entryTS: "tools/users.calendars.events.list.ts",
			want: []tooldef.ResourceParam{
				{Name: "user_id", BindingName: "user_id"},
				{Name: "calendar_id", BindingName: "calendar_id"},
			},
		},
		{
			name:    "nested resources delete includes deepest",
			entryTS: "tools/users.calendars.events.delete.ts",
			want: []tooldef.ResourceParam{
				{Name: "user_id", BindingName: "user_id"},
				{Name: "calendar_id", BindingName: "calendar_id"},
				{Name: "event_id", BindingName: "event_id"},
			},
		},
		{
			name:    "simple tool no resources",
			entryTS: "tools/calc.add.ts",
			want:    nil,
		},
		{
			name:    "single segment verb only",
			entryTS: "tools/search.ts",
			want:    nil,
		},
		{
			name:    "path with directories",
			entryTS: "tools/zendesk/account.tickets.update.ts",
			want: []tooldef.ResourceParam{
				{Name: "account_id", BindingName: "account_id"},
				{Name: "ticket_id", BindingName: "ticket_id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := InferResourceParams(tt.entryTS)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("InferResourceParams(%q) mismatch (-want +got):\n%s", tt.entryTS, diff)
			}
		})
	}
}
