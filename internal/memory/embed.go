package memory

import "embed"

// Migrations holds the schema, applied at start.
//
//go:embed migrations/*.sql
var Migrations embed.FS
