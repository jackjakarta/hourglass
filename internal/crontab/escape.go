package crontab

import "strings"

// EscapePercent escapes every % as \%. Cron turns an unescaped % in a
// command into a newline and feeds the rest to stdin.
func EscapePercent(s string) string { return strings.ReplaceAll(s, "%", `\%`) }

// UnescapePercent is the inverse of EscapePercent: it turns \% into %,
// scanning left to right.
func UnescapePercent(s string) string {
	if !strings.Contains(s, `\%`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
