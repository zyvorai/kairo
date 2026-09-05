package sim

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseDocuments parses JSON or the conservative YAML subset used by Kubernetes
// object manifests. It intentionally supports maps, sequences, quoted/unquoted
// scalars, inline []/{}, comments, and multi-document streams without external
// dependencies. Advanced YAML features such as anchors are rejected.
func ParseDocuments(src string) ([]map[string]any, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, nil
	}
	if strings.HasPrefix(src, "{") || strings.HasPrefix(src, "[") {
		var v any
		if err := json.Unmarshal([]byte(src), &v); err != nil {
			return nil, err
		}
		switch t := v.(type) {
		case map[string]any:
			return []map[string]any{t}, nil
		case []any:
			out := make([]map[string]any, 0, len(t))
			for _, x := range t {
				m, ok := x.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("JSON array item is not an object")
				}
				out = append(out, m)
			}
			return out, nil
		}
	}
	parts := splitYAMLDocuments(src)
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		v, err := parseYAMLDocument(part)
		if err != nil {
			return nil, err
		}
		if len(v) > 0 {
			out = append(out, v)
		}
	}
	return out, nil
}

func splitYAMLDocuments(src string) []string {
	var parts []string
	var b strings.Builder
	s := bufio.NewScanner(strings.NewReader(src))
	for s.Scan() {
		line := s.Text()
		if strings.TrimSpace(line) == "---" {
			if strings.TrimSpace(b.String()) != "" {
				parts = append(parts, b.String())
			}
			b.Reset()
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if strings.TrimSpace(b.String()) != "" {
		parts = append(parts, b.String())
	}
	return parts
}

type yamlLine struct {
	indent int
	text   string
	line   int
}

func parseYAMLDocument(src string) (map[string]any, error) {
	lines := []yamlLine{}
	s := bufio.NewScanner(strings.NewReader(src))
	lineNo := 0
	for s.Scan() {
		lineNo++
		raw := strings.TrimRight(s.Text(), " \t\r")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "..." {
			continue
		}
		if strings.Contains(raw, "\t") {
			return nil, fmt.Errorf("line %d: tabs are not supported in YAML indentation", lineNo)
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		text := stripComment(strings.TrimSpace(raw))
		if text == "" {
			continue
		}
		lines = append(lines, yamlLine{indent, text, lineNo})
	}
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	v, next, err := parseBlock(lines, 0, lines[0].indent)
	if err != nil {
		return nil, err
	}
	if next != len(lines) {
		return nil, fmt.Errorf("line %d: unexpected YAML content", lines[next].line)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("top-level YAML document must be a mapping")
	}
	return m, nil
}

func parseBlock(lines []yamlLine, idx, indent int) (any, int, error) {
	if idx >= len(lines) {
		return map[string]any{}, idx, nil
	}
	if lines[idx].indent != indent {
		return nil, idx, fmt.Errorf("line %d: unexpected indentation", lines[idx].line)
	}
	if strings.HasPrefix(lines[idx].text, "- ") || lines[idx].text == "-" {
		return parseSequence(lines, idx, indent)
	}
	return parseMap(lines, idx, indent)
}

func parseMap(lines []yamlLine, idx, indent int) (map[string]any, int, error) {
	m := map[string]any{}
	for idx < len(lines) {
		ln := lines[idx]
		if ln.indent < indent {
			break
		}
		if ln.indent > indent {
			return nil, idx, fmt.Errorf("line %d: unexpected indentation", ln.line)
		}
		if strings.HasPrefix(ln.text, "- ") || ln.text == "-" {
			break
		}
		key, rest, ok := splitKeyValue(ln.text)
		if !ok {
			return nil, idx, fmt.Errorf("line %d: expected key: value", ln.line)
		}
		if key == "<<" {
			return nil, idx, fmt.Errorf("line %d: YAML merge keys are not supported", ln.line)
		}
		idx++
		if strings.TrimSpace(rest) != "" {
			m[unquote(key)] = parseScalar(rest)
			continue
		}
		if idx < len(lines) && lines[idx].indent > indent {
			childIndent := lines[idx].indent
			child, next, err := parseBlock(lines, idx, childIndent)
			if err != nil {
				return nil, idx, err
			}
			m[unquote(key)] = child
			idx = next
		} else {
			m[unquote(key)] = nil
		}
	}
	return m, idx, nil
}

func parseSequence(lines []yamlLine, idx, indent int) ([]any, int, error) {
	arr := []any{}
	for idx < len(lines) {
		ln := lines[idx]
		if ln.indent < indent {
			break
		}
		if ln.indent != indent || !(strings.HasPrefix(ln.text, "- ") || ln.text == "-") {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(ln.text, "-"))
		idx++
		if rest == "" {
			if idx < len(lines) && lines[idx].indent > indent {
				child, next, err := parseBlock(lines, idx, lines[idx].indent)
				if err != nil {
					return nil, idx, err
				}
				arr = append(arr, child)
				idx = next
			} else {
				arr = append(arr, nil)
			}
			continue
		}
		if key, val, ok := splitKeyValue(rest); ok {
			item := map[string]any{}
			if strings.TrimSpace(val) != "" {
				item[unquote(key)] = parseScalar(val)
			} else if idx < len(lines) && lines[idx].indent > indent {
				child, next, err := parseBlock(lines, idx, lines[idx].indent)
				if err != nil {
					return nil, idx, err
				}
				item[unquote(key)] = child
				idx = next
			} else {
				item[unquote(key)] = nil
			}
			// Additional mapping keys belonging to this sequence item.
			if idx < len(lines) && lines[idx].indent > indent && !(strings.HasPrefix(lines[idx].text, "- ") || lines[idx].text == "-") {
				moreIndent := lines[idx].indent
				more, next, err := parseMap(lines, idx, moreIndent)
				if err != nil {
					return nil, idx, err
				}
				for k, v := range more {
					item[k] = v
				}
				idx = next
			}
			arr = append(arr, item)
		} else {
			arr = append(arr, parseScalar(rest))
		}
	}
	return arr, idx, nil
}

func splitKeyValue(s string) (string, string, bool) {
	inSingle, inDouble := false, false
	depthSquare, depthCurly := 0, 0
	for i, r := range s {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '[':
			if !inSingle && !inDouble {
				depthSquare++
			}
		case ']':
			if !inSingle && !inDouble {
				depthSquare--
			}
		case '{':
			if !inSingle && !inDouble {
				depthCurly++
			}
		case '}':
			if !inSingle && !inDouble {
				depthCurly--
			}
		case ':':
			if !inSingle && !inDouble && depthSquare == 0 && depthCurly == 0 {
				if i+1 == len(s) || s[i+1] == ' ' {
					return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
				}
			}
		}
	}
	return "", "", false
}

func stripComment(s string) string {
	inSingle, inDouble := false, false
	for i, r := range s {
		if r == '\'' && !inDouble {
			inSingle = !inSingle
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
		}
		if r == '#' && !inSingle && !inDouble && (i == 0 || s[i-1] == ' ') {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

func parseScalar(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		var v any
		if json.Unmarshal([]byte(strings.ReplaceAll(s, "'", "\"")), &v) == nil {
			return v
		}
	}
	if (strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) || (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
		return unquote(s)
	}
	switch strings.ToLower(s) {
	case "null", "~":
		return nil
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}
