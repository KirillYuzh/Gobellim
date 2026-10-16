package main

import (
	"fmt"
	"regexp"
	"sort"
)

type ID struct {
	Series string
	Num    int
}

func (i ID) String() string {
	switch i.Series {
	case seriesBR:
		return fmt.Sprintf("BR-%02d", i.Num)
	case seriesI:
		return fmt.Sprintf("I-%02d", i.Num)
	default:
		return fmt.Sprintf("D%d", i.Num)
	}
}

const (
	seriesI  = "I"
	seriesBR = "BR"
	seriesD  = "D"
)

var tokenRe = regexp.MustCompile(`\b(BR-\d{1,3}|I-\d{1,3}|D\d{1,3})\b`)

var (
	defRowRe  = regexp.MustCompile(`^\s*\|\s*(BR-\d{1,3}|I-\d{1,3}|D\d{1,3})\s*\|`)
	defHeadRe = regexp.MustCompile(`^#{1,6}\s+(BR-\d{1,3}|I-\d{1,3}|D\d{1,3})\s*:?\s*$`)
)

var idRe = regexp.MustCompile(`^(BR|I|D)-?(\d{1,3})$`)

func parseID(raw string) (ID, bool) {
	m := idRe.FindStringSubmatch(raw)
	if m == nil {
		return ID{}, false
	}
	n := 0
	for _, r := range m[2] {
		n = n*10 + int(r-'0')
	}
	return ID{Series: m[1], Num: n}, true
}

func idsIn(line string) []ID {
	var out []ID
	for _, m := range tokenRe.FindAllString(line, -1) {
		if id, ok := parseID(m); ok {
			out = append(out, id)
		}
	}
	return out
}

func isDefinitionLine(line string) bool {
	return defRowRe.MatchString(line) || defHeadRe.MatchString(line)
}

func definedID(line string) (ID, bool) {
	if m := defRowRe.FindStringSubmatch(line); m != nil {
		return parseID(m[1])
	}
	if m := defHeadRe.FindStringSubmatch(line); m != nil {
		return parseID(m[1])
	}
	return ID{}, false
}

type idSet map[string]map[int]bool

func newIDSet() idSet { return idSet{} }

func (s idSet) add(id ID) {
	m, ok := s[id.Series]
	if !ok {
		m = map[int]bool{}
		s[id.Series] = m
	}
	m[id.Num] = true
}

func (s idSet) has(id ID) bool {
	m, ok := s[id.Series]
	if !ok {
		return false
	}
	return m[id.Num]
}

func (s idSet) maxOf(series string) int {
	m := s[series]
	best := -1
	for n := range m {
		if n > best {
			best = n
		}
	}
	return best
}

func (s idSet) nums(series string) []int {
	m := s[series]
	out := make([]int, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
