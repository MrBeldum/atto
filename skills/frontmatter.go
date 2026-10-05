package skills

import (
	"errors"
	"strconv"
	"strings"
)

// ParseFrontmatter splits a markdown file into its YAML frontmatter and body,
// like pi's parseFrontmatter. atto has no YAML dependency, so only what skill
// files use is understood: top-level "key: value" pairs with plain, quoted or
// block (| and >) scalars; nested structures are ignored. Without a
// frontmatter block the map is empty and the body is the whole text.
func ParseFrontmatter(content string) (map[string]string, string, error) {
	text := strings.TrimPrefix(content, string(rune(0xFEFF))) // BOM
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	fm := map[string]string{}
	if !strings.HasPrefix(text, "---") {
		return fm, text, nil
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return fm, text, nil
	}
	end += 3
	body := strings.TrimSpace(text[end+4:])
	if end < 4 {
		return fm, body, nil // an empty block
	}
	return fm, body, parseYAML(text[4:end], fm)
}

func parseYAML(src string, out map[string]string) error {
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
			continue // part of a nested value we do not model
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok || key == "" || (val != "" && val[0] != ' ' && val[0] != '\t') {
			return errors.New("invalid YAML frontmatter: " + strconv.Quote(line))
		}
		val = strings.TrimSpace(val)
		// Indented lines after the key continue its value.
		var cont []string
		for i+1 < len(lines) && (lines[i+1] == "" || lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
			i++
			cont = append(cont, strings.TrimSpace(lines[i]))
		}
		for len(cont) > 0 && cont[len(cont)-1] == "" {
			cont = cont[:len(cont)-1]
		}
		switch {
		case val != "" && (val[0] == '|' || val[0] == '>'):
			sep := "\n"
			if val[0] == '>' {
				sep = " "
			}
			out[key] = strings.Join(cont, sep)
		case val == "":
			out[key] = strings.Join(cont, " ")
		default:
			v, err := scalar(val)
			if err != nil {
				return err
			}
			if len(cont) > 0 { // a plain or quoted scalar folded over lines
				v = strings.Join(append([]string{v}, cont...), " ")
			}
			out[key] = v
		}
	}
	return nil
}

func scalar(v string) (string, error) {
	switch v[0] {
	case '"':
		if u, err := strconv.Unquote(v); err == nil {
			return u, nil
		}
		if len(v) >= 2 && v[len(v)-1] == '"' {
			return v[1 : len(v)-1], nil
		}
		return "", errors.New("invalid YAML frontmatter: unterminated string " + v)
	case '\'':
		if len(v) >= 2 && v[len(v)-1] == '\'' {
			return strings.ReplaceAll(v[1:len(v)-1], "''", "'"), nil
		}
		return "", errors.New("invalid YAML frontmatter: unterminated string " + v)
	}
	if i := strings.Index(v, " #"); i >= 0 { // a trailing comment
		v = strings.TrimSpace(v[:i])
	}
	return v, nil
}
