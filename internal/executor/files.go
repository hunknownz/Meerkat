package executor

import (
	"github.com/hunknownz/Meerkat/internal/platform"
	"os"
)

func openNoFollow(path string) (*os.File, error) { return platform.OpenFile(path, os.O_RDONLY, 0) }
