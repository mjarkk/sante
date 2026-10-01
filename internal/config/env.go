package config

import (
	"fmt"
	"strings"
)

// expand interpolates environment references the way POSIX shells and Docker
// Compose do:
//
//	$VAR, ${VAR}       value of VAR, empty when unset
//	${VAR:-default}    default when VAR is unset or empty
//	${VAR-default}     default when VAR is unset
//	${VAR:?message}    error when VAR is unset or empty
//	${VAR?message}     error when VAR is unset
//	$$                 a literal $
//
// Defaults may themselves contain references. A $ that does not start a
// reference is kept as is. Names of variables referenced without a default
// that are unset are reported to missing.
func expand(s string, lookup func(string) (string, bool), missing func(string)) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '$' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		switch next := s[i+1]; {
		case next == '$':
			b.WriteByte('$')
			i++
		case next == '{':
			end := closingBrace(s, i+2)
			if end < 0 {
				return "", fmt.Errorf("unterminated ${ in %q", s)
			}
			v, err := substitute(s[i+2:end], lookup, missing)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
			i = end
		case isNameStart(next):
			j := i + 1
			for j < len(s) && isNameChar(s[j]) {
				j++
			}
			name := s[i+1 : j]
			v, ok := lookup(name)
			if !ok {
				missing(name)
			}
			b.WriteString(v)
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}

func closingBrace(s string, from int) int {
	depth := 1
	for i := from; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func substitute(expr string, lookup func(string) (string, bool), missing func(string)) (string, error) {
	n := 0
	for n < len(expr) && (n == 0 && isNameStart(expr[n]) || n > 0 && isNameChar(expr[n])) {
		n++
	}
	name, rest := expr[:n], expr[n:]
	if name == "" {
		return "", fmt.Errorf("invalid variable reference ${%s}", expr)
	}
	value, set := lookup(name)

	var op string
	for _, candidate := range []string{":-", ":?", "-", "?"} {
		if strings.HasPrefix(rest, candidate) {
			op = candidate
			break
		}
	}
	if op == "" && rest != "" {
		return "", fmt.Errorf("invalid variable reference ${%s}", expr)
	}
	arg := rest[len(op):]

	switch op {
	case "":
		if !set {
			missing(name)
		}
		return value, nil
	case ":-", "-":
		if set && (op == "-" || value != "") {
			return value, nil
		}
		return expand(arg, lookup, missing)
	default:
		if set && (op == "?" || value != "") {
			return value, nil
		}
		msg, err := expand(arg, lookup, missing)
		if err != nil {
			return "", err
		}
		if msg == "" {
			msg = "is required"
		}
		return "", fmt.Errorf("environment variable %s %s", name, msg)
	}
}

func isNameStart(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isNameChar(c byte) bool {
	return isNameStart(c) || '0' <= c && c <= '9'
}
