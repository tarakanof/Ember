package producer

import (
	"fmt"
	"os"
)

// DefaultLogThreshold is the size (10 MiB) at which producers rotate a log.
const DefaultLogThreshold int64 = 10 * 1024 * 1024

const logRotateGenerations = 5

// RotateLogIfLarge renames path -> path.1 (shifting .1..N) when it exceeds
// threshold bytes, keeping logRotateGenerations generations. Best-effort:
// rename failures are ignored.
func RotateLogIfLarge(path string, threshold int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < threshold {
		return
	}
	for i := logRotateGenerations - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	_ = os.Rename(path, path+".1")
}
