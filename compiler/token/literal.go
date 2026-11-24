package token

import (
	"fmt"
	"strconv"
	"strings"
)

// Unescape decodes the escape sequences allowed in WayLang string and rune
// literals: \\ \" \' \n \t \r \0 \xNN \u{NNNN}.
func Unescape(s string) (string, error) {
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", fmt.Errorf("trailing backslash in literal")
		}
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case '\'':
			b.WriteByte('\'')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case 'x':
			if i+2 >= len(s) {
				return "", fmt.Errorf("incomplete \\x escape")
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return "", fmt.Errorf("invalid \\x escape %q", s[i+1:i+3])
			}
			b.WriteByte(byte(v))
			i += 2
		case 'u':
			if i+1 >= len(s) || s[i+1] != '{' {
				return "", fmt.Errorf("expected \\u{...} escape")
			}
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated \\u{...} escape")
			}
			hex := s[i+2 : i+2+end]
			v, err := strconv.ParseUint(hex, 16, 32)
			if err != nil {
				return "", fmt.Errorf("invalid unicode escape %q", hex)
			}
			b.WriteRune(rune(v))
			i += end + 2
		default:
			return "", fmt.Errorf("unknown escape sequence \\%c", s[i])
		}
	}
	return b.String(), nil
}
