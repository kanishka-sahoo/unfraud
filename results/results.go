// Package results embeds the published benchmark results so cases-api can
// serve them; regenerate them with the commands in each file.
package results

import "embed"

//go:embed *.md *.json
var FS embed.FS
