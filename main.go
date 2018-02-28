package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

var (
	dir           = flag.String("dir", "", "watched directory")
	build         = flag.String("build", "", "build command")
	run           = flag.String("run", "", "run command")
	coalesceSleep = flag.Duration("coalesce", 100*time.Millisecond,
		"how long to sleep accumulating events")

	starting = make(chan struct{})
	done     = make(chan struct{})
	notify   = make(chan struct{}, 10)

	mu            sync.Mutex
	running       *exec.Cmd
	running_stdin io.Closer
)

func main() {
	if args := os.Getenv("LIVE_EXEC_CHILD"); args != "" {
		become(args)
		panic("unreachable")
	}

	flag.Parse()

	if *build == "" && *run == "" {
		flag.Usage()
		os.Exit(1)
	}

	// kick off a build/run
	if err := attemptBuild(); err == nil {
		attemptRun()
	}

	go watch()
	waiting_start := false // if we're waiting for a start
	waiting_done := false  // if we're waiting for the build/run to be done

	for {
		var notify_ch chan struct{}
		if !waiting_done {
			notify_ch = notify
		}

		select {
		case <-notify_ch:
			if waiting_start {
				break
			}
			waiting_start = true
			go signaled()

		case <-starting:
			waiting_start = false
			waiting_done = true

		case <-done:
			waiting_done = false
		}
	}
}

//
// because not all platforms support pdeathsig, we spawn ourselves again as
// a helper process that listens on stdin from the parent. if stdin is ever
// closed for any reason, we kill the child and exit ourselves.
//

func command(args string) (*exec.Cmd, io.Closer) {
	pr, pw, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	cmd := exec.Command(os.Args[0], "child-monitor")
	cmd.Stdin = pr
	cmd.Env = append(os.Environ(), fmt.Sprintf("LIVE_EXEC_CHILD=%s", args))
	setProcAttr(cmd.SysProcAttr)

	return cmd, pw
}

func become(args string) {
	cmd := exec.Command("sh", "-c", args)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setProcAttr(cmd.SysProcAttr)

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// TODO: there has to be a way to kill the whole process group by spawning
	// the command in bash or something.

	go func() {
		os.Stdin.Read(make([]byte, 1))
		cmd.Process.Kill()
		os.Exit(0)
	}()

	if err := cmd.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)

	panic("unreachable")
}

//
// keep track of a running command
//

func set(cmd *exec.Cmd, stdin io.Closer) {
	mu.Lock()
	defer mu.Unlock()

	if running != nil {
		fmt.Printf("--- killing old process pid: %d\n", running.Process.Pid)
		running_stdin.Close()
		running.Process.Wait()

		running = nil
		running_stdin = nil
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("--- error starting process: %+v\n", err)
	} else {
		fmt.Printf("--- process started with pid: %d\n", cmd.Process.Pid)

		running = cmd
		running_stdin = stdin
		go wait(cmd)
	}
}

func wait(cmd *exec.Cmd) {
	cmd.Wait()

	mu.Lock()
	defer mu.Unlock()

	fmt.Printf("--- process exited with pid: %d\n", cmd.Process.Pid)

	if cmd != running {
		return
	}
	running = nil
}

//
// watch sends on notify whenever a line comes in from fswatch
//

func watch() {
	cmd, _ := command(fmt.Sprintf(`fswatch "%s"`, *dir))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	lines := bufio.NewScanner(stdout)
	for lines.Scan() {
		select {
		case notify <- struct{}{}:
		default:
			fmt.Println("--- missed notification")
		}
	}
	if err := lines.Err(); err != nil {
		panic(err)
	}
	panic("fswatch exited unexpectedly")
}

//
// signaled waits to start, and then attempts the build and run, sending on
// done when it's done.
//

func signaled() {
	time.Sleep(*coalesceSleep)

	starting <- struct{}{}
	defer func() { done <- struct{}{} }()

	if err := attemptBuild(); err != nil {
		return
	}
	attemptRun()
}

func attemptBuild() (err error) {
	if *build == "" {
		return nil
	}

	fmt.Println("--- attempting build...")
	cmd, _ := command(*build)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func attemptRun() {
	if *run == "" {
		return
	}

	fmt.Println("--- attempting run...")
	cmd, stdin := command(*run)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	set(cmd, stdin)
}
