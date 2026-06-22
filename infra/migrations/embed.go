// Package migrations embeds the golang-migrate SQL files so clowk-hep3
// ships as a single binary with no external migration files to mount.
// The files follow golang-migrate's NNNNNN_name.{up,down}.sql convention
// and are read via the source/iofs driver.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
