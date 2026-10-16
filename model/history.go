package model

type Counter interface{ Count(text string) int }

type ByteCounter struct{}

func (ByteCounter) Count(text string) int { return len(text) / 4 }

type History struct {
	Counter  Counter
	Budget   int
	KeepLast int
}

func (h History) Trim(msgs []Message) []Message {
	if h.Budget <= 0 || h.Counter == nil || len(msgs) == 0 {
		return msgs
	}
	keepLast := h.KeepLast
	if keepLast <= 0 {
		keepLast = 2
	}

	segs := segmentMessages(msgs)
	total := 0
	for _, s := range segs {
		total += s.cost(h.Counter)
	}
	if total <= h.Budget {
		return msgs
	}

	protectedFrom := protectedSuffix(segs, keepLast)
	last := len(segs) - 1
	kept := make([]bool, len(segs))
	for i := range kept {
		kept[i] = true
	}

	for drop := 0; total > h.Budget && drop < len(segs); drop++ {
		if segs[drop].pinned || drop >= protectedFrom || drop == last {
			continue
		}
		kept[drop] = false
		total -= segs[drop].cost(h.Counter)
	}

	out := make([]Message, 0, len(msgs))
	for i, s := range segs {
		if kept[i] {
			out = append(out, s.msgs...)
		}
	}
	return out
}

type segment struct {
	msgs   []Message
	pinned bool
}

func (s segment) cost(c Counter) int {
	total := 0
	for _, m := range s.msgs {
		total += messageCost(c, m)
	}
	return total
}

func messageCost(c Counter, m Message) int {
	n := c.Count(m.Content) + c.Count(m.Name)
	for _, tc := range m.ToolCalls {
		n += c.Count(tc.Name) + c.Count(tc.Args)
	}
	return n
}

func segmentMessages(msgs []Message) []segment {
	segs := make([]segment, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == RoleSystem {
			segs = append(segs, segment{msgs: []Message{m}, pinned: true})
			continue
		}
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			group := []Message{m}
			j := i + 1
			for j < len(msgs) && msgs[j].Role == RoleTool {
				group = append(group, msgs[j])
				j++
			}
			segs = append(segs, segment{msgs: group})
			i = j - 1
			continue
		}
		segs = append(segs, segment{msgs: []Message{m}})
	}
	return segs
}

func protectedSuffix(segs []segment, keepLast int) int {
	idx := len(segs)
	sum := 0
	for i := len(segs) - 1; i >= 0; i-- {
		if sum >= keepLast {
			break
		}
		sum += len(segs[i].msgs)
		idx = i
	}
	return idx
}
