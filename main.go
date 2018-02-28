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
	notify   = make(chan struct{})

	mu            sync.Mutex
	running       *exec.Cmd
	running_stdin io.Closer
)

func main() {
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
// command launches the command in such a way that if its stdin is closed, the
// child is killed.
//

func command(args string) (*exec.Cmd, io.Closer) {
	pr, pw, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	cmd := exec.Command("bash", "-c",
		fmt.Sprintf("(%s)& (read)& wait -n; kill $(jobs -p)", args))
	cmd.Stdin = pr

	return cmd, pw
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
	fmt.Println("--- modification detected...")
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
