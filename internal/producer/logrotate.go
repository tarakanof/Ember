package producer

import (
	"context"
	"time"

	"fmt"
	"golang.org/x/sys/unix"
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

const LogCheckInterval = time.Hour

func RotateOpenLog(path string, threshold int64) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() < threshold {
		return false
	}
	RotateLogIfLarge(path, threshold)
	nf, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false
	}
	defer nf.Close()
	fd := int(nf.Fd())
	_ = unix.Dup2(fd, 1)
	_ = unix.Dup2(fd, 2)
	if cur := os.Stdout; cur != nil && int(cur.Fd()) > 2 {
		_ = unix.Dup2(fd, int(cur.Fd()))
	}
	return true
}

func WatchLog(ctx context.Context, path string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			RotateOpenLog(path, DefaultLogThreshold)
		}
	}
}
