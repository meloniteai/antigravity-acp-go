package antigravityacp

import (
	"strings"
)

var narrationPrefixes = []string{"I will", "I'll", "I’ll"}

func IsNarration(text string) bool {
	lines := strings.Split(text, "\n")
	var filtered []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			filtered = append(filtered, l)
		}
	}
	if len(filtered) == 0 {
		return false
	}
	for _, l := range filtered {
		line := strings.TrimLeft(l, " \t\r")
		isNarr := false
		for _, prefix := range narrationPrefixes {
			if strings.HasPrefix(line, prefix) {
				isNarr = true
				break
			}
		}
		if !isNarr {
			return false
		}
	}
	return true
}

func FilterNarration(parts []string) string {
	var nonNarr []string
	for _, p := range parts {
		if !IsNarration(p) {
			nonNarr = append(nonNarr, p)
		}
	}
	return strings.Join(nonNarr, "\n")
}
