package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"
)

var (
	dir        = flag.String("dir", "", "watched directory")
	build      = flag.String("build", "", "build command")
	run        = flag.String("run", "", "run command")
	quiescence = flag.Duration("quiescence", 100*time.Millisecond,
		"quiescence period after a change before triggering build")

	starting = make(chan struct{})
	done     = make(chan struct{})
	notify   = make(chan struct{})

	running      *exec.Cmd
	running_done = make(chan struct{})
)

func main() {
	flag.Parse()

	if *build == "" && *run == "" {
		flag.Usage()
		os.Exit(1)
	}

	attempt()

	go watch()

	var (
		timer   *time.Timer
		waiting bool
	)

	for {
		var timer_ch <-chan time.Time
		if timer != nil {
			timer_ch = timer.C
		}

		select {
		case <-timer_ch:
			fmt.Println("--- quiescence reached")
			go attempt()
			timer = nil
			waiting = false

		case <-notify:
			if !waiting {
				fmt.Println("--- modification detected...")
				waiting = true
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(*quiescence)
		}
	}
}

func watch() {
	cmd := exec.Command("fswatch", *dir)
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

func attempt() {
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
	cmd := exec.Command(*build)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func attemptRun() {
	if *run == "" {
		return
	}

	fmt.Println("--- attempting run...")
	cmd := exec.Command(*run)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if running != nil {
		fmt.Printf("--- killing old process pid: %d\n", running.Process.Pid)
		running.Process.Kill()
		running.Wait()
		<-running_done
		running = nil
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("--- error starting process: %+v\n", err)
	} else {
		fmt.Printf("--- process started with pid: %d\n", cmd.Process.Pid)
		running = cmd
		go func() {
			cmd.Wait()
			fmt.Printf("--- process exited with pid: %d\n", cmd.Process.Pid)
			running_done <- struct{}{}
		}()
	}
}
