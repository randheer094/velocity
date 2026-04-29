// Command print-version-major prints the velocity binary's compile-time
// Major version constant to stdout. Used by scripts/check-major.sh so
// the release CI gate doesn't have to grep Go source files (which
// would break on any reformatting of the constant declaration).
package main

import (
	"fmt"

	"github.com/randheer094/velocity/internal/version"
)

func main() {
	fmt.Println(version.Major)
}
