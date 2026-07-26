package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Field describes one editable config leaf for the dashboard form builder.
type Field struct {
	Path    string   `json:"path"`
	Label   string   `json:"label"`
	Group   string   `json:"group"`
	Type    string   `json:"type"` // string|number|boolean|string[]|object
	Default any      `json:"default"`
	Secret  bool     `json:"secret"`
	Enum    []string `json:"enum,omitempty"`
	Help    string   `json:"help,omitempty"`
}

var enums = map[string][]string{
	"database.driver":           {"sqlite", "postgres", "memory"},
	"rag.provider":              {"builtin", "enowx", "none"},
	"terminal.backend":          {"local", "docker", "ssh"},
	"tools.approval_mode":       {"auto", "prompt", "deny"},
	"session_reset.mode":        {"never", "idle", "daily"},
	"display.theme":             {"system", "light", "dark"},
	"logging.level":             {"debug", "info", "warn", "error"},
	"agent.reasoning_effort":    {"none", "low", "medium", "high"},
	"model.reasoning_effort":    {"none", "low", "medium", "high"},
	"tools.web_search.provider": {"duckduckgo", "brave", "tavily", "searxng", "none"},
}

var help = map[string]string{
	"database.driver":       "Backend penyimpanan. sqlite untuk single-node, postgres untuk multi-node.",
	"database.dsn":          "sqlite: path file. postgres: postgres://user:pass@host:5432/db?sslmode=disable",
	"rag.provider":          "builtin memakai vector store internal; enowx memakai daemon enowx-rag.",
	"compression.threshold": "Rasio pemakaian context window sebelum kompaksi otomatis berjalan.",
	"server.auth_token":     "Kosongkan untuk auto-generate saat start pertama.",
}

func secretKey(path string) bool {
	l := strings.ToLower(path)
	for _, s := range []string{"api_key", "token", "secret", "password", "dsn"} {
		if strings.Contains(l, s) {
			return l != "database.dsn" || strings.Contains(l, "password")
		}
	}
	return false
}

func humanize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	parts := strings.Fields(s)
	for i, p := range parts {
		switch strings.ToLower(p) {
		case "url", "dsn", "api", "ttl", "id", "ws", "mcp", "rag", "cwd", "ssh", "cors", "wal":
			parts[i] = strings.ToUpper(p)
		default:
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// Schema walks the default config and produces a flat, ordered field list.
func Schema() []Field {
	var out []Field
	walk(reflect.ValueOf(*Default()), "", "", &out)
	return out
}

func walk(v reflect.Value, prefix, group string, out *[]Field) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("yaml")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		path := name
		grp := group
		if prefix != "" {
			path = prefix + "." + name
		} else {
			grp = name
		}
		fv := v.Field(i)

		switch fv.Kind() {
		case reflect.Struct:
			walk(fv, path, grp, out)
			continue
		case reflect.Map:
			*out = append(*out, Field{Path: path, Label: humanize(name), Group: grp, Type: "object", Default: fv.Interface()})
			continue
		case reflect.Slice:
			*out = append(*out, Field{Path: path, Label: humanize(name), Group: grp, Type: "string[]", Default: fv.Interface(), Help: help[path]})
			continue
		}

		f := Field{
			Path:    path,
			Label:   humanize(name),
			Group:   grp,
			Default: fv.Interface(),
			Secret:  secretKey(path),
			Enum:    enums[path],
			Help:    help[path],
		}
		switch fv.Kind() {
		case reflect.Bool:
			f.Type = "boolean"
		case reflect.Int, reflect.Int64, reflect.Float64:
			f.Type = "number"
		default:
			f.Type = "string"
		}
		*out = append(*out, f)
	}
}

// GetPath reads a dotted config path, e.g. "model.default".
func (c *Config) GetPath(path string) (any, error) {
	v, err := resolve(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return nil, err
	}
	return v.Interface(), nil
}

// SetPath writes a dotted config path, coercing the string form when needed.
func (c *Config) SetPath(path string, value any) error {
	v, err := resolve(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return err
	}
	if !v.CanSet() {
		return fmt.Errorf("config path %q is not writable", path)
	}
	return assign(v, value)
}

func resolve(v reflect.Value, path string) (reflect.Value, error) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		if cur.Kind() == reflect.Map {
			key := reflect.ValueOf(seg)
			got := cur.MapIndex(key)
			if !got.IsValid() {
				return reflect.Value{}, fmt.Errorf("unknown config key %q", seg)
			}
			// Maps are not addressable; callers use SetMapPath for these.
			cur = got
			continue
		}
		if cur.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("config path %q is not a struct", seg)
		}
		found := false
		t := cur.Type()
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
			if name == seg {
				cur = cur.Field(i)
				found = true
				break
			}
		}
		if !found {
			return reflect.Value{}, fmt.Errorf("unknown config key %q", seg)
		}
	}
	return cur, nil
}

func assign(dst reflect.Value, value any) error {
	if value == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}
	rv := reflect.ValueOf(value)
	if rv.Type().AssignableTo(dst.Type()) {
		dst.Set(rv)
		return nil
	}
	if rv.Type().ConvertibleTo(dst.Type()) && dst.Kind() != reflect.Slice && dst.Kind() != reflect.Map {
		dst.Set(rv.Convert(dst.Type()))
		return nil
	}

	s := fmt.Sprint(value)
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("expected boolean, got %q", s)
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("expected number, got %q", s)
		}
		dst.SetInt(int64(n))
	case reflect.Float64:
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("expected number, got %q", s)
		}
		dst.SetFloat(n)
	case reflect.Slice, reflect.Map:
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out := reflect.New(dst.Type())
		if err := json.Unmarshal(b, out.Interface()); err != nil {
			return fmt.Errorf("expected %s: %w", dst.Type(), err)
		}
		dst.Set(out.Elem())
	default:
		return fmt.Errorf("unsupported config type %s", dst.Type())
	}
	return nil
}

// Redacted returns a deep copy with secret values masked, for API responses.
func (c *Config) Redacted() map[string]any {
	b, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	redactMap(m, "")
	return m
}

func redactMap(m map[string]any, prefix string) {
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch t := v.(type) {
		case map[string]any:
			redactMap(t, path)
		case string:
			if t != "" && secretKey(path) {
				m[k] = maskSecret(t)
			}
		}
	}
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "••••••••"
	}
	return s[:4] + strings.Repeat("•", 8) + s[len(s)-4:]
}
