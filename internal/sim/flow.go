// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package sim

import "strings"

// maxFlowDepth bounds nesting so untrusted input cannot exhaust the stack.
const maxFlowDepth = 32

// parseFlow parses YAML flow collections with plain (unquoted) keys and
// values, such as `{name: web, ports: [80, 443]}`. It is used when the text is
// not valid JSON. ok is false when s is not a complete, well-formed collection.
func parseFlow(s string) (any, bool) {
	p := &flowParser{s: strings.TrimSpace(s)}
	v, ok := p.value(0)
	if !ok {
		return nil, false
	}
	p.skip()
	if p.i != len(p.s) {
		return nil, false
	}
	return v, true
}

type flowParser struct {
	s string
	i int
}

func (p *flowParser) skip() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *flowParser) value(depth int) (any, bool) {
	if depth > maxFlowDepth {
		return nil, false
	}
	p.skip()
	if p.i >= len(p.s) {
		return nil, false
	}
	switch p.s[p.i] {
	case '{':
		return p.mapping(depth)
	case '[':
		return p.sequence(depth)
	case '"', '\'':
		return p.quoted()
	}
	return p.plain(false)
}

func (p *flowParser) mapping(depth int) (any, bool) {
	p.i++ // {
	m := map[string]any{}
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return m, true
	}
	for {
		p.skip()
		var key string
		if p.i < len(p.s) && (p.s[p.i] == '"' || p.s[p.i] == '\'') {
			k, ok := p.quoted()
			if !ok {
				return nil, false
			}
			key, _ = k.(string)
		} else {
			k, ok := p.plain(true)
			if !ok {
				return nil, false
			}
			key = str(k)
		}
		p.skip()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, false
		}
		p.i++
		v, ok := p.value(depth + 1)
		if !ok {
			return nil, false
		}
		m[key] = v
		p.skip()
		if p.i >= len(p.s) {
			return nil, false
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return m, true
		default:
			return nil, false
		}
	}
}

func (p *flowParser) sequence(depth int) (any, bool) {
	p.i++ // [
	arr := []any{}
	p.skip()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return arr, true
	}
	for {
		v, ok := p.value(depth + 1)
		if !ok {
			return nil, false
		}
		arr = append(arr, v)
		p.skip()
		if p.i >= len(p.s) {
			return nil, false
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return arr, true
		default:
			return nil, false
		}
	}
}

func (p *flowParser) quoted() (any, bool) {
	q := p.s[p.i]
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == q {
			if q == '\'' && p.i+1 < len(p.s) && p.s[p.i+1] == '\'' { // '' escape
				b.WriteByte('\'')
				p.i += 2
				continue
			}
			p.i++
			return b.String(), true
		}
		if c == '\\' && q == '"' && p.i+1 < len(p.s) {
			p.i++
			switch p.s[p.i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(p.s[p.i])
			}
			p.i++
			continue
		}
		b.WriteByte(c)
		p.i++
	}
	return nil, false
}

// plain reads an unquoted scalar up to a flow delimiter. As a key it also
// stops at ':'; as a value a ':' only ends it when followed by a space.
func (p *flowParser) plain(isKey bool) (any, bool) {
	start := p.i
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == ',' || c == '}' || c == ']' {
			break
		}
		if c == ':' && (isKey || p.i+1 >= len(p.s) || p.s[p.i+1] == ' ') {
			break
		}
		if c == '{' || c == '[' {
			return nil, false
		}
		p.i++
	}
	raw := strings.TrimSpace(p.s[start:p.i])
	if raw == "" {
		return nil, false
	}
	if isKey {
		return raw, true
	}
	return parseScalar(raw), true
}
