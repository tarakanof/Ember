package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var goRuns = newProcessGroups()

type processGroups struct {
	interrupted context.Context
	interrupt   context.CancelFunc
	liveMu      sync.Mutex
	live        int
	idle        *sync.Cond
}

func newProcessGroups() *processGroups {
	g := &processGroups{}
	g.interrupted, g.interrupt = context.WithCancel(context.Background())
	g.idle = sync.NewCond(&g.liveMu)
	return g
}

func (g *processGroups) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmdCtx, cancel := context.WithCancel(ctx)
	stopForwarding := context.AfterFunc(g.interrupted, cancel)
	g.liveMu.Lock()
	g.live++
	g.liveMu.Unlock()
	context.AfterFunc(ctx, func() {
		stopForwarding()
		cancel()
		g.liveMu.Lock()
		g.live--
		g.idle.Broadcast()
		g.liveMu.Unlock()
	})
	cmd := exec.CommandContext(cmdCtx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcessGroup(cmd.Process.Pid) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (g *processGroups) killAll() {
	g.interrupt()
	g.liveMu.Lock()
	for g.live > 0 {
		g.idle.Wait()
	}
	g.liveMu.Unlock()
}

func killProcessGroup(pgid int) error {
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func goRunCommand(ctx context.Context, args ...string) *exec.Cmd {
	return goRuns.command(ctx, "go", append([]string{"run", "."}, args...)...)
}

func TestProcessGroupCancelReportsProcessDoneOnceTheGroupIsGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := newProcessGroups().command(ctx, "sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Cancel after the group exited = %v, want os.ErrProcessDone", err)
	}
}

func TestProcessGroupsKillAllKillsTheWholeGroupOfARunningCommand(t *testing.T) {
	g := newProcessGroups()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := g.command(ctx, "sh", "-c", "sleep 60 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() {
		waited <- cmd.Wait()
		cancel()
	}()
	killed := make(chan struct{})
	go func() {
		g.killAll()
		close(killed)
	}()
	select {
	case <-killed:
	case <-time.After(20 * time.Second):
		t.Fatal("killAll did not return")
	}
	if err := <-waited; err == nil {
		t.Fatal("sh exited cleanly, want it killed")
	}
	waitFor(t, "the orphaned sleep to be reaped", func() bool { return errors.Is(syscall.Kill(child, 0), syscall.ESRCH) })
}

func TestInterruptingTheTestBinaryKillsItsGoRunGroups(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	helper := newProcessGroups().command(ctx, os.Args[0], "-test.run=^TestProcessGroupsHelperHoldsASleep$", "-test.count=1")
	helper.Env = append(os.Environ(), "PROCESS_GROUPS_HELPER=1")
	out, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	child := readSleepPID(t, bufio.NewScanner(out))
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err := syscall.Kill(helper.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := helper.Wait(); err == nil {
		t.Fatal("interrupted test binary exited 0")
	}
	waitFor(t, "the interrupted binary's sleep to die", func() bool { return errors.Is(syscall.Kill(child, 0), syscall.ESRCH) })
}

func TestProcessGroupsHelperHoldsASleep(t *testing.T) {
	if os.Getenv("PROCESS_GROUPS_HELPER") != "1" {
		t.Skip("helper for TestInterruptingTheTestBinaryKillsItsGoRunGroups")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := goRuns.command(ctx, "sh", "-c", "sleep 60 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("sleep-pid=%s", line)
	_ = cmd.Wait()
}

func readSleepPID(t *testing.T, sc *bufio.Scanner) int {
	t.Helper()
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "sleep-pid="); ok {
			pid, err := strconv.Atoi(v)
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatalf("helper printed no sleep pid: %v", sc.Err())
	return 0
}
