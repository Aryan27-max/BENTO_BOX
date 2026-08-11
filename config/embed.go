// Package config embeds Bento's dependency and profile definitions into the
// binary. Embedding is what makes the released executable genuinely
// self-contained: a user runs one file and it already knows about every tool
// Bento supports, with no data files to ship alongside it.
package config

import "embed"

// FS holds the profile list and every dependency definition.
//
//go:embed profiles.json dependencies/*.json
var FS embed.FS

// ProfilesFile is the path of the profile definitions within FS.
const ProfilesFile = "profiles.json"

// DependenciesGlob matches every dependency definition file within FS.
const DependenciesGlob = "dependencies/*.json"
