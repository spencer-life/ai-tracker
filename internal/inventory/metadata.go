package inventory

import (
	"bytes"
	"errors"
	"strings"
)

type declaration struct {
	kind    string
	name    string
	enabled *bool
	count   int
}

// jsonDeclarations is a purpose-built structural parser. It retains object key
// names, booleans, and array counts; all string/number/null values are skipped.
func jsonDeclarations(data []byte) ([]declaration, error) {
	p := jsonMetaParser{data: data}
	if err := p.value(nil, 0); err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(data) {
		return nil, errors.New("trailing JSON data")
	}
	return p.decls, nil
}

type jsonMetaParser struct {
	data  []byte
	i     int
	decls []declaration
}

func (p *jsonMetaParser) space() {
	for p.i < len(p.data) && bytes.ContainsRune([]byte(" \t\r\n"), rune(p.data[p.i])) {
		p.i++
	}
}

func (p *jsonMetaParser) value(path []string, depth int) error {
	if depth > maxNesting {
		return errors.New("JSON nesting limit exceeded")
	}
	p.space()
	if p.i >= len(p.data) {
		return errors.New("unexpected end of JSON")
	}
	switch p.data[p.i] {
	case '{':
		return p.object(path, depth+1)
	case '[':
		return p.array(path, depth+1)
	case '"':
		_, err := p.string(false)
		return err
	default:
		start := p.i
		for p.i < len(p.data) && !bytes.ContainsRune([]byte(",]} \t\r\n"), rune(p.data[p.i])) {
			p.i++
		}
		if p.i == start {
			return errors.New("invalid JSON value")
		}
		return nil
	}
}

func (p *jsonMetaParser) object(path []string, depth int) error {
	p.i++
	for entries := 0; ; entries++ {
		if entries > maxEntries {
			return errors.New("JSON object entry limit exceeded")
		}
		p.space()
		if p.i < len(p.data) && p.data[p.i] == '}' {
			p.i++
			return nil
		}
		if entries > 0 {
			if p.i >= len(p.data) || p.data[p.i] != ',' {
				return errors.New("expected JSON comma")
			}
			p.i++
			p.space()
		}
		key, err := p.string(true)
		if err != nil {
			return err
		}
		p.space()
		if p.i >= len(p.data) || p.data[p.i] != ':' {
			return errors.New("expected JSON colon")
		}
		p.i++
		next := append(append([]string(nil), path...), key)
		if kind, ok := recognizedContainer(path); ok {
			d := declaration{kind: kind, name: key}
			switch strings.ToLower(path[len(path)-1]) {
			case "enabledplugins":
				v := true
				d.enabled = &v
			case "disabledplugins":
				v := false
				d.enabled = &v
			}
			p.space()
			if bytes.HasPrefix(p.data[p.i:], []byte("true")) {
				v := true
				d.enabled = &v
			}
			if bytes.HasPrefix(p.data[p.i:], []byte("false")) {
				v := false
				d.enabled = &v
			}
			// Arrays are recorded after parsing so their element count can be
			// reported without retaining any element values.
			if p.i >= len(p.data) || p.data[p.i] != '[' {
				p.decls = append(p.decls, d)
			}
		}
		if err := p.value(next, depth); err != nil {
			return err
		}
	}
}

func (p *jsonMetaParser) array(path []string, depth int) error {
	p.i++
	count := 0
	for {
		p.space()
		if p.i < len(p.data) && p.data[p.i] == ']' {
			p.i++
			break
		}
		if count > 0 {
			if p.i >= len(p.data) || p.data[p.i] != ',' {
				return errors.New("expected array comma")
			}
			p.i++
		}
		if count >= maxEntries {
			return errors.New("JSON array entry limit exceeded")
		}
		if err := p.value(path, depth); err != nil {
			return err
		}
		count++
	}
	if kind, ok := recognizedContainer(path[:max(0, len(path)-1)]); ok && len(path) > 0 {
		p.decls = append(p.decls, declaration{kind: kind, name: path[len(path)-1], count: count})
	}
	return nil
}

func (p *jsonMetaParser) string(retain bool) (string, error) {
	p.space()
	if p.i >= len(p.data) || p.data[p.i] != '"' {
		return "", errors.New("expected JSON string")
	}
	p.i++
	var b strings.Builder
	for p.i < len(p.data) {
		c := p.data[p.i]
		p.i++
		if c == '"' {
			return b.String(), nil
		}
		if c == '\\' {
			if p.i >= len(p.data) {
				return "", errors.New("invalid JSON escape")
			}
			e := p.data[p.i]
			p.i++
			if retain && e != 'u' {
				b.WriteByte(e)
			}
			if e == 'u' {
				if p.i+4 > len(p.data) {
					return "", errors.New("invalid unicode escape")
				}
				p.i += 4
				if retain {
					b.WriteByte('?')
				}
			}
		} else if retain {
			b.WriteByte(c)
		}
	}
	return "", errors.New("unterminated JSON string")
}

func recognizedContainer(path []string) (string, bool) {
	// Supported declarations are top-level configuration containers. Nested
	// objects inside one declaration often contain keys such as "hooks" or
	// "command" that are fields, not additional components.
	if len(path) != 1 {
		return "", false
	}
	switch strings.ToLower(path[len(path)-1]) {
	case "mcpservers", "mcp_servers":
		return "mcp_server", true
	case "hooks":
		return "hook", true
	case "plugins", "enabledplugins", "disabledplugins":
		return "plugin", true
	case "agents", "subagents":
		return "agent", true
	case "skills":
		return "skill", true
	default:
		return "", false
	}
}

func tomlDeclarations(data []byte) []declaration {
	var out []declaration
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) < 3 || line[0] != '[' {
			continue
		}
		end := bytes.IndexByte(line, ']')
		if end < 0 {
			continue
		}
		// Convert only the table header. Assignment values, including command
		// and environment bodies, are never converted or retained.
		section := strings.TrimSpace(strings.Trim(string(line[1:end]), "[]"))
		parts := strings.Split(section, ".")
		if len(parts) < 2 {
			continue
		}
		container := strings.ToLower(strings.Trim(parts[0], " \"'"))
		name := strings.Trim(parts[1], " \"'")
		kind := ""
		switch container {
		case "mcp_servers", "mcpservers":
			kind = "mcp_server"
		case "hooks":
			kind = "hook"
		case "plugins":
			kind = "plugin"
		case "agents":
			kind = "agent"
		case "skills":
			kind = "skill"
		}
		if kind != "" && name != "" {
			out = append(out, declaration{kind: kind, name: name})
		}
	}
	return out
}
