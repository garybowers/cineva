package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"go.yaml.in/yaml/v3"

	"github.com/garybowers/cineva/server/docs"
)

// The spec is the contract: every route must be in it, and everything in it
// must be a route. Without this the two drift silently — endpoints have gone
// undocumented for whole milestones before anyone noticed.
func TestSpecMatchesRoutes(t *testing.T) {
	e := echo.New()
	(&server{}).apiRoutes(e)

	param := regexp.MustCompile(`:(\w+)`)
	routes := map[string]bool{}
	for _, r := range e.Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/") || r.Method == "echo_route_not_found" {
			continue
		}
		path := param.ReplaceAllString(strings.TrimPrefix(r.Path, "/api/v1"), "{$1}")
		routes[strings.ToUpper(r.Method)+" "+path] = true
	}

	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "put", "post", "delete", "patch":
				// The spec names parameters as it likes; compare shapes.
				documented[strings.ToUpper(method)+" "+path] = true
			}
		}
	}

	shape := regexp.MustCompile(`\{\w+\}`)
	norm := func(set map[string]bool) map[string]string {
		out := map[string]string{}
		for k := range set {
			out[shape.ReplaceAllString(k, "{}")] = k
		}
		return out
	}
	have, want := norm(routes), norm(documented)

	var undocumented, phantom []string
	for k, orig := range have {
		if _, ok := want[k]; !ok {
			undocumented = append(undocumented, orig)
		}
	}
	for k, orig := range want {
		if _, ok := have[k]; !ok {
			phantom = append(phantom, orig)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(phantom)
	for _, r := range undocumented {
		t.Errorf("route not in docs/openapi.yaml: %s", r)
	}
	for _, r := range phantom {
		t.Errorf("documented but not a route: %s", r)
	}
}
