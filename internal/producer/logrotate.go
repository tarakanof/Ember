package producer

import (
	"fmt"
	"os"
)

const DefaultLogThreshold int64 = 10 * 1024 * 1024

const logRotateGenerations = 5

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
