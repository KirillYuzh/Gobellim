package main

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var inlineLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

var refDefRe = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*(\S+)`)

type link struct {
	target string
	text   string
}

func linksIn(line string) []link {
	var out []link
	if m := refDefRe.FindStringSubmatch(line); m != nil {
		out = append(out, link{target: m[1], text: strings.TrimSpace(m[0])})
	}
	for _, m := range inlineLinkRe.FindAllStringSubmatch(line, -1) {
		out = append(out, link{target: m[1], text: m[0]})
	}
	return out
}

func external(target string) bool {
	switch {
	case strings.HasPrefix(target, "http://"),
		strings.HasPrefix(target, "https://"),
		strings.HasPrefix(target, "mailto:"),
		strings.HasPrefix(target, "tel:"),
		strings.HasPrefix(target, "#!"):
		return true
	}
	return false
}

func splitTarget(target string) (file, anchor string) {
	if i := strings.Index(target, "#"); i >= 0 {
		return target[:i], target[i+1:]
	}
	return target, ""
}

func slugify(heading string) string {
	heading = strings.TrimSpace(heading)
	heading = strings.NewReplacer("`", "", "*", "", "_", "").Replace(heading)
	if i := strings.Index(heading, " ["); i >= 0 {
		heading = heading[:i]
	}
	heading = strings.ToLower(heading)
	var b strings.Builder
	for _, r := range heading {
		switch {
		case r == ' ' || r == '\t':
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

func anchorsOf(content string) map[string]bool {
	out := map[string]bool{}
	seen := map[string]int{}
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		h := strings.TrimLeft(t, "#")
		if h == "" || h[0] != ' ' {
			continue
		}
		slug := slugify(h)
		if slug == "" {
			continue
		}
		seen[slug]++
		out[slug] = true
		if n := seen[slug]; n > 1 {
			out[slug+"-"+itoa(n-1)] = true
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func decodeAnchor(anchor string) string {
	if u, err := url.PathUnescape(anchor); err == nil {
		anchor = u
	}
	return strings.ToLower(anchor)
}
