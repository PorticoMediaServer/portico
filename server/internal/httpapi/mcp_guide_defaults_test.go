package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestMCPGuideDefaultWindow(t *testing.T) {
	f := administrationFixtureNew(t)
	store, err := livechannels.New(f.deps.DB)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.LiveChannels = store
	f.handler = New(f.deps)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"mcp-guide-window","name":"Guide","scopes":["read"]}`)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(time.Hour).Format(time.RFC3339)
	end := now.Add(4 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		name    string
		args    map[string]string
		invalid bool
	}{
		{name: "default", args: map[string]string{}},
		{name: "start only", args: map[string]string{"start": start}},
		{name: "end only", args: map[string]string{"end": end}},
		{name: "explicit", args: map[string]string{"start": start, "end": end}},
		{name: "malformed start", args: map[string]string{"start": "not-a-time"}, invalid: true},
		{name: "reversed", args: map[string]string{"start": end, "end": start}, invalid: true},
		{name: "window too large", args: map[string]string{"start": start, "end": now.Add(26 * time.Hour).Format(time.RFC3339)}, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "tv_guide", "arguments": tc.args}})
			_, answer := f.mcp(t, key.Secret, string(body))
			if answer.Error != nil {
				t.Fatalf("RPC: %+v", answer.Error)
			}
			if answer.Result.IsError != tc.invalid {
				t.Fatalf("isError=%v: %+v", answer.Result.IsError, answer.Result.Content)
			}
			if tc.invalid {
				return
			}
			guide, ok := answer.Result.StructuredContent["guide"].(map[string]any)
			if !ok {
				t.Fatal("guide missing from result")
			}
			from, e1 := time.Parse(time.RFC3339, guide["start"].(string))
			to, e2 := time.Parse(time.RFC3339, guide["end"].(string))
			if e1 != nil || e2 != nil || from.Nanosecond() != 0 || to.Nanosecond() != 0 {
				t.Fatal("window is not whole-second RFC 3339")
			}
			if tc.args["start"] != "" && guide["start"] != tc.args["start"] {
				t.Fatal("explicit start was changed")
			}
			if tc.args["end"] != "" && guide["end"] != tc.args["end"] {
				t.Fatal("explicit end was changed")
			}
			if tc.args["start"] == "" && (from.Before(now) || from.After(time.Now().UTC())) {
				t.Fatal("default start is not current time")
			}
			if tc.args["end"] == "" && to.Sub(from) != 3*time.Hour {
				t.Fatal("default end is not three hours after start")
			}
		})
	}
}
