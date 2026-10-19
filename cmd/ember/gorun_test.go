package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const goRunDrainBound = 5 * time.Second

var errGroupsInterrupted = errors.New("process groups interrupted")

var goRuns = newProcessGroups()

type processGroups struct {
	liveMu      sync.Mutex
	live        map[int]bool
	interrupted bool
}

func newProcessGroups() *processGroups {
	return &processGroups{live: map[int]bool{}}
}

type groupCmd struct {
	*exec.Cmd
	groups *processGroups
	// Output and CombinedOutput shadow exec.Cmd's, whose inner Run would skip Start's tracking.
	Output, CombinedOutput struct{}
}

func (g *processGroups) command(ctx context.Context, name string, args ...string) *groupCmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcessGroup(cmd.Process.Pid) }
	cmd.WaitDelay = 5 * time.Second
	return &groupCmd{Cmd: cmd, groups: g}
}

func (c *groupCmd) Start() error {
	c.groups.liveMu.Lock()
	defer c.groups.liveMu.Unlock()
	if c.groups.interrupted {
		return errGroupsInterrupted
	}
	if err := c.Cmd.Start(); err != nil {
		return err
	}
	c.groups.live[c.Process.Pid] = true
	return nil
}

func (c *groupCmd) Wait() error {
	err := c.Cmd.Wait()
	if c.Process == nil {
		return err
	}
	c.groups.liveMu.Lock()
	delete(c.groups.live, c.Process.Pid)
	c.groups.liveMu.Unlock()
	return err
}

func (c *groupCmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

func (g *processGroups) killAll(bound time.Duration) bool {
	g.liveMu.Lock()
	g.interrupted = true
	for pgid := range g.live {
		_ = killProcessGroup(pgid)
	}
	g.liveMu.Unlock()
	deadline := time.Now().Add(bound)
	for {
		g.liveMu.Lock()
		n := len(g.live)
		g.liveMu.Unlock()
		if n == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func killProcessGroup(pgid int) error {
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func goRunCommand(ctx context.Context, args ...string) *groupCmd {
	return goRuns.command(ctx, "go", append([]string{"run", "."}, args...)...)
}

func processGone(t *testing.T, pid int) bool {
	t.Helper()
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("ps: %v", err)
	}
	stat := strings.TrimSpace(string(out))
	return stat == "" || strings.HasPrefix(stat, "Z")
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("pid %d to die", pid), func() bool { return processGone(t, pid) })
}

const groupedSleep = "sleep 20 & echo $!; wait"

func startGroupedSleep(t *testing.T, cmd *groupCmd) int {
	t.Helper()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	t.Cleanup(func() {
		if t.Failed() {
			_ = killProcessGroup(pgid)
		}
	})
	pid, err := readPID(bufio.NewScanner(out), "")
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func readPID(sc *bufio.Scanner, prefix string) (int, error) {
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), prefix); ok {
			return strconv.Atoi(v)
		}
	}
	return 0, fmt.Errorf("no %q pid line: %v", prefix, sc.Err())
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

func TestProcessGroupsKillAllWaitsForAnUncancelledCommandToExit(t *testing.T) {
	g := newProcessGroups()
	cmd := g.command(context.Background(), "sh", "-c", groupedSleep)
	child := startGroupedSleep(t, cmd)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	if !g.killAll(goRunDrainBound) {
		t.Fatal("killAll did not drain")
	}
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("sh exited cleanly, want it killed")
		}
	default:
		t.Fatal("killAll returned before Wait did")
	}
	waitGone(t, child)
}

func TestProcessGroupsKillAllKillsDespiteAStuckCancelAndGivesUpAfterTheBound(t *testing.T) {
	g := newProcessGroups()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := g.command(ctx, "sh", "-c", groupedSleep)
	release := make(chan struct{})
	cmd.Cancel = func() error {
		<-release
		return nil
	}
	child := startGroupedSleep(t, cmd)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	defer func() {
		close(release)
		<-waited
	}()
	cancel()
	drained := make(chan bool, 1)
	go func() { drained <- g.killAll(200 * time.Millisecond) }()
	select {
	case ok := <-drained:
		if ok {
			t.Fatal("killAll drained while Wait was stuck in Cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("killAll is not bounded")
	}
	waitGone(t, child)
}

func TestProcessGroupsRefuseToStartAfterKillAll(t *testing.T) {
	g := newProcessGroups()
	g.killAll(0)
	if err := g.command(context.Background(), "sh", "-c", "exit 0").Run(); !errors.Is(err, errGroupsInterrupted) {
		t.Fatalf("Run after killAll = %v, want errGroupsInterrupted", err)
	}
}

func TestGroupCmdWaitWithoutAStartedProcessReturnsTheError(t *testing.T) {
	g := newProcessGroups()
	g.killAll(0)
	cmd := g.command(context.Background(), "sh", "-c", "exit 0")
	if err := cmd.Start(); !errors.Is(err, errGroupsInterrupted) {
		t.Fatalf("Start = %v, want errGroupsInterrupted", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("Wait without a started process returned nil")
	}
}

func TestGroupCmdExposesNoUntrackedRunMethods(t *testing.T) {
	typ := reflect.TypeFor[*groupCmd]()
	for _, name := range []string{"Output", "CombinedOutput"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("groupCmd.%s is callable; exec.Cmd's version would run the command without tracking its group", name)
		}
	}
}

func TestProcessGoneTreatsAnUnreapedZombieAsDead(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()
	waitFor(t, "the unreaped child to exit", func() bool { return processGone(t, cmd.Process.Pid) })
	if errors.Is(syscall.Kill(cmd.Process.Pid, 0), syscall.ESRCH) {
		t.Fatal("child was reaped early; the zombie case went untested")
	}
}

type groupsHelper struct {
	cmd      *groupCmd
	sleepPID int
	waited   bool
}

func startGroupsHelper(t *testing.T, mode string, args ...string) *groupsHelper {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	h := &groupsHelper{cmd: newProcessGroups().command(ctx, os.Args[0], append([]string{"-test.run=^TestProcessGroupsHelper$", "-test.count=1"}, args...)...)}
	h.cmd.Env = append(os.Environ(), "PROCESS_GROUPS_HELPER="+mode)
	out, err := h.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !h.waited {
			_ = syscall.Kill(h.cmd.Process.Pid, syscall.SIGINT)
			_ = h.cmd.Wait()
		}
	})
	if h.sleepPID, err = readPID(bufio.NewScanner(out), "sleep-pid="); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *groupsHelper) wait() error {
	h.waited = true
	return h.cmd.Wait()
}

func TestInterruptingTheTestBinaryKillsItsGoRunGroups(t *testing.T) {
	h := startGroupsHelper(t, "hold")
	if err := syscall.Kill(h.cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := h.wait(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("interrupted test binary: %v, want exit status 1", err)
	}
	waitGone(t, h.sleepPID)
}

func TestASecondInterruptKillsATestBinaryStuckDraining(t *testing.T) {
	h := startGroupsHelper(t, "stuck")
	if err := syscall.Kill(h.cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitGone(t, h.sleepPID)
	if err := syscall.Kill(h.cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var exit *exec.ExitError
	err := h.wait()
	if !errors.As(err, &exit) {
		t.Fatalf("helper: %v, want killed by SIGINT", err)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Fatalf("helper exited %v after %v, want killed by the second SIGINT", err, time.Since(start))
	}
}

func TestATestTimeoutPanicKillsItsGoRunGroups(t *testing.T) {
	h := startGroupsHelper(t, "hold", "-test.timeout=5s")
	t.Cleanup(func() {
		if pgid, err := syscall.Getpgid(h.sleepPID); err == nil && t.Failed() {
			_ = killProcessGroup(pgid)
		}
	})
	var exit *exec.ExitError
	if err := h.wait(); !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("timed-out test binary: %v, want the exit status 2 of a -test.timeout panic", err)
	}
	waitGone(t, h.sleepPID)
}

func TestProcessGroupsHelper(t *testing.T) {
	mode := os.Getenv("PROCESS_GROUPS_HELPER")
	if mode == "" {
		t.Skip("helper for the interrupt tests")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := goRuns.command(ctx, "sh", "-c", groupedSleep)
	if mode == "stuck" {
		cmd.Cancel = func() error {
			time.Sleep(time.Hour)
			return nil
		}
	}
	child := startGroupedSleep(t, cmd)
	fmt.Printf("sleep-pid=%d\n", child)
	if mode == "stuck" {
		cancel()
	}
	_ = cmd.Wait()
	time.Sleep(time.Minute)
	t.Fatal("helper was not interrupted")
}
