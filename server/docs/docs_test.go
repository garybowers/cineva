package docs

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The spec is served as-is to the reference page and to SDK generators, so
// a mistake in it breaks both without any build noticing. These checks are
// the ones a broken edit has actually needed: it parses, every internal
// reference points at something, and every operation is complete.
func TestOpenAPISpec(t *testing.T) {
	var spec map[string]any
	if err := yaml.Unmarshal(OpenAPI, &spec); err != nil {
		t.Fatalf("openapi.yaml does not parse: %v", err)
	}
	if v, _ := spec["openapi"].(string); !strings.HasPrefix(v, "3.1") {
		t.Fatalf("openapi = %q, want 3.1.x", v)
	}

	// Every "$ref": "#/a/b/c" must resolve within the document.
	var refs []string
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			for k, child := range n {
				if s, ok := child.(string); ok && k == "$ref" {
					refs = append(refs, s)
				}
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(spec)
	for _, ref := range refs {
		if err := resolve(spec, ref); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}

	// Every operation has a unique operationId, at least one response, and
	// one of the declared tags, which is how the reference page groups it.
	declaredTags := map[string]bool{}
	tagList, _ := spec["tags"].([]any)
	for _, tg := range tagList {
		m, _ := tg.(map[string]any)
		name, _ := m["name"].(string)
		declaredTags[name] = true
	}
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}
	seen := map[string]string{}
	paths, _ := spec["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("no paths")
	}
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for method, op := range ops {
			if !methods[method] {
				continue
			}
			where := strings.ToUpper(method) + " " + path
			o, _ := op.(map[string]any)
			id, _ := o["operationId"].(string)
			if id == "" {
				t.Errorf("%s: no operationId", where)
			} else if prev, dup := seen[id]; dup {
				t.Errorf("%s: operationId %q already used by %s", where, id, prev)
			} else {
				seen[id] = where
			}
			if r, _ := o["responses"].(map[string]any); len(r) == 0 {
				t.Errorf("%s: no responses", where)
			}
			if tags, _ := o["tags"].([]any); len(tags) != 1 {
				t.Errorf("%s: wants exactly one tag, has %d", where, len(tags))
			} else if name, _ := tags[0].(string); !declaredTags[name] {
				t.Errorf("%s: tag %q is not in the top-level tags list", where, name)
			}
			// Every {name} in the path is declared, at the path or the
			// operation level, or clients cannot fill it in.
			declared := map[string]bool{}
			for _, list := range []any{ops["parameters"], o["parameters"]} {
				params, _ := list.([]any)
				for _, p := range params {
					pm, _ := p.(map[string]any)
					if pm["in"] == "path" {
						name, _ := pm["name"].(string)
						declared[name] = true
					}
				}
			}
			for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
				if !declared[m[1]] {
					t.Errorf("%s: path parameter {%s} is not declared", where, m[1])
				}
			}
		}
	}
}

var pathParam = regexp.MustCompile(`\{(\w+)\}`)

func resolve(spec map[string]any, ref string) error {
	if !strings.HasPrefix(ref, "#/") {
		return fmt.Errorf("not an internal reference")
	}
	var node any = spec
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return fmt.Errorf("%q is not an object", part)
		}
		if node, ok = m[part]; !ok {
			return fmt.Errorf("nothing at %q", part)
		}
	}
	return nil
}
