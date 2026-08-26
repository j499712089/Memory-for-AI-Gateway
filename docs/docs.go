// Package docs embeds the OpenAPI specification and the self-contained Swagger
// UI assets into the Gateway binary. The Gateway serves only these resources
// under /docs — the rest of the docs directory (architecture, database schema,
// deployment guides) is deliberately not exposed (ALL-272).
package docs

import "embed"

//go:embed openapi.yaml swagger-ui/*
var Content embed.FS
