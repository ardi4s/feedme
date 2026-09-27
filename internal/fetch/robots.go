package fetch

import (
	"strconv"
	"strings"
)

// Robots is a parsed robots.txt for one host.
type Robots struct {
	// Allow and Disallow hold the rules for the group that matched our
	// user agent, in file order.
	Allow    []string
	Disallow []string
	// CrawlDelay is in seconds; 0 when absent.
	CrawlDelay float64
	Sitemaps   []string
	// Found is false when robots.txt was absent (which means "allow all").
	Found bool
}

// ParseRobots parses robots.txt content.
//
// Group selection follows the usual convention: the most specific user-agent
// token that matches wins, and only that group's rules apply. When no token
// matches, the "*" group is used.
func ParseRobots(content string) *Robots {
	r := &Robots{Found: true}
	type rule struct {
		field    string
		value    string
		allow    bool
		delay    float64
		hasDelay bool
	}
	groups := make([][]rule, 0)
	current := make([]rule, 0)
	lastWasAgent := false

	for _, line := range strings.Split(content, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		field := strings.ToLower(strings.TrimSpace(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])

		switch field {
		case "user-agent":
			// Consecutive User-agent lines share one rule group. A blank
			// line or any other directive closes the group.
			if !lastWasAgent && len(current) > 0 {
				groups = append(groups, current)
				current = make([]rule, 0)
			}
			current = append(current, rule{field: "user-agent", value: value})
			lastWasAgent = true
		case "allow":
			current = append(current, rule{field: "allow", value: value, allow: true})
			lastWasAgent = false
		case "disallow":
			current = append(current, rule{field: "disallow", value: value})
			lastWasAgent = false
		case "crawl-delay":
			d, _ := strconv.ParseFloat(value, 64)
			current = append(current, rule{field: "crawl-delay", delay: d, hasDelay: true})
			lastWasAgent = false
		case "sitemap":
			// Sitemap is global, not per-group.
			if value != "" {
				r.Sitemaps = append(r.Sitemaps, value)
			}
			lastWasAgent = false
		default:
			lastWasAgent = false
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}

	ua := strings.ToLower(agentToken)
	best := -1
	bestLen := -1
	for i, g := range groups {
		for _, rl := range g {
			if rl.field != "user-agent" {
				continue
			}
			tok := strings.ToLower(rl.value)
			if tok == "" {
				continue
			}
			if strings.Contains(ua, tok) && len(tok) > bestLen {
				best, bestLen = i, len(tok)
			}
		}
	}
	if best < 0 {
		for i, g := range groups {
			for _, rl := range g {
				if rl.field == "user-agent" && rl.value == "*" {
					best = i
					break
				}
			}
			if best >= 0 {
				break
			}
		}
	}
	if best < 0 {
		return r
	}
	for _, rl := range groups[best] {
		switch rl.field {
		case "allow":
			if rl.value != "" {
				r.Allow = append(r.Allow, rl.value)
			}
		case "disallow":
			// "Disallow:" with an empty value means allow everything.
			if rl.value != "" {
				r.Disallow = append(r.Disallow, rl.value)
			}
		case "crawl-delay":
			r.CrawlDelay = rl.delay
		}
	}
	return r
}

// patternMatch matches a robots path pattern (supporting * and $) against a
// path. Returns the length of the matched prefix, which callers use to pick
// the most specific rule.
func patternMatch(pattern, path string) int {
	p, u := pattern, path
	// A leading $ anchors the pattern to the end of the URL.
	anchoredEnd := false
	if strings.HasSuffix(p, "$") {
		anchoredEnd = true
		p = strings.TrimSuffix(p, "$")
	}
	segments := strings.Split(p, "*")
	if len(segments) == 1 {
		if !strings.Contains(p, "*") {
			if anchoredEnd {
				if u == p {
					return len(p)
				}
				return -1
			}
			if strings.HasPrefix(u, p) {
				return len(p)
			}
			return -1
		}
	}
	// Wildcard case: walk the segments in order.
	pos := 0
	matched := 0
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		if i == 0 {
			idx := strings.Index(u, seg)
			if idx != 0 {
				return -1
			}
			pos = len(seg)
			matched = pos
			continue
		}
		idx := strings.Index(u[pos:], seg)
		if idx < 0 {
			return -1
		}
		pos += idx + len(seg)
		matched = pos
	}
	if anchoredEnd && pos != len(u) {
		return -1
	}
	return matched
}

// Allowed reports whether path may be fetched. The longest matching rule wins;
// Allow beats Disallow on equal length, per the de facto standard.
func (r *Robots) Allowed(path string) bool {
	if r == nil {
		return true
	}
	bestLen := -1
	bestAllow := true
	for _, p := range r.Disallow {
		if n := patternMatch(p, path); n > bestLen {
			bestLen, bestAllow = n, false
		}
	}
	for _, p := range r.Allow {
		if n := patternMatch(p, path); n > bestLen {
			bestLen, bestAllow = n, true
		}
	}
	return bestAllow
}
