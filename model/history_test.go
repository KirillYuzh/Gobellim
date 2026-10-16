package model

import "testing"

type runeCounter struct{}

func (runeCounter) Count(text string) int { return len([]rune(text)) }

func contents(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestByteCounter(t *testing.T) {
	c := ByteCounter{}
	if got := c.Count("abcdefgh"); got != 2 {
		t.Fatalf("ByteCounter.Count(\"abcdefgh\") = %d, want 2", got)
	}
	if got := c.Count("abc"); got != 0 {
		t.Fatalf("ByteCounter.Count(\"abc\") = %d, want 0", got)
	}
}

func TestHistoryTrimDisabled(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "a"}, {Role: RoleUser, Content: "b"}}
	if got := (History{Counter: runeCounter{}}).Trim(msgs); len(got) != 2 {
		t.Fatalf("budget 0 must disable trimming, got %d messages", len(got))
	}
	if got := (History{Budget: 1}).Trim(msgs); len(got) != 2 {
		t.Fatalf("nil Counter must disable trimming, got %d messages", len(got))
	}
}

func TestHistoryTrimDropsOldestKeepsSystemAndLast(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "SYS"},
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, Content: "a1"},
		{Role: RoleUser, Content: "u2"},
		{Role: RoleAssistant, Content: "a2"},
	}
	h := History{Counter: runeCounter{}, Budget: 7, KeepLast: 2}
	got := contents(h.Trim(msgs))
	want := []string{"SYS", "u2", "a2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestHistoryTrimNeverOrphansToolCalls(t *testing.T) {
	msgs := func() []Message {
		return []Message{
			{Role: RoleSystem, Content: "SYS"},
			{Role: RoleUser, Content: "old question that is long enough to drop"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "f", Args: `{"x":1}`}}},
			{Role: RoleTool, ToolCallID: "1", Content: "tool result"},
			{Role: RoleUser, Content: "new"},
			{Role: RoleAssistant, Content: "done"},
		}
	}
	groups := func(msgs []Message) (toolCalls, toolResults int) {
		for _, m := range msgs {
			if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
				toolCalls++
			}
			if m.Role == RoleTool {
				toolResults++
			}
		}
		return
	}

	tight := History{Counter: runeCounter{}, Budget: 26, KeepLast: 2}.Trim(msgs())
	if tc, tr := groups(tight); tc != 0 || tr != 0 {
		t.Fatalf("tight trim orphaned a tool group: tool_calls=%d tool_results=%d in %v", tc, tr, contents(tight))
	}

	loose := History{Counter: runeCounter{}, Budget: 55, KeepLast: 2}.Trim(msgs())
	if tc, tr := groups(loose); tc != 1 || tr != 1 {
		t.Fatalf("loose trim split the tool group: tool_calls=%d tool_results=%d in %v", tc, tr, contents(loose))
	}
}

func TestHistoryTrimAlwaysKeepsLastMessage(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "one"},
		{Role: RoleUser, Content: "two"},
		{Role: RoleAssistant, Content: "three"},
	}
	got := History{Counter: runeCounter{}, Budget: 1, KeepLast: 1}.Trim(msgs)
	if len(got) == 0 || got[len(got)-1].Content != "three" {
		t.Fatalf("last message dropped: %v", contents(got))
	}
}
