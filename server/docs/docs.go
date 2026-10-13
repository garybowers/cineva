// Package docs embeds the API specification, so the server can serve it —
// and the reference page built on it — from the binary itself.
package docs

import _ "embed"

// OpenAPI is openapi.yaml: the API's contract, served at /api/v1/openapi.yaml
// and rendered at /api/docs.
//
//go:embed openapi.yaml
var OpenAPI []byte
