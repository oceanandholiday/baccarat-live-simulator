// Package redact removes database URLs from error text before logging.
package redact

import (
	"regexp"
	"strings"
)

var urlPattern = regexp.MustCompile(`(?i)postgres(?:ql)?://\S+`)

// Error returns err with any postgres URL replaced. A nil error returns "".
func Error(err error) string {
	if err == nil {
		return ""
	}
	return urlPattern.ReplaceAllString(err.Error(), "postgres://REDACTED")
}

// String redacts postgres URLs in an arbitrary log field.
func String(s string) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	return urlPattern.ReplaceAllString(s, "postgres://REDACTED")
}
