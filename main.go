package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cespare/fswatch"
)

var (
	dir        = flag.String("dir", ".", "watched directory")
	build      = flag.String("build", "", "build command")
	run        = flag.String("run", "", "run command")
	quiescence = flag.Duration("quiescence", 100*time.Millisecond,
		"quiescence period after a change before triggering build")
)

func logf(format string, args ...interface{}) {
	fmt.Printf("--- %s\n", fmt.Sprintf(format, args...))
}

func main() {
	flag.Parse()
	if *build == "" && *run == "" {
		fmt.Fprintln(os.Stderr, "error: must specify run and/or build.")
		flag.Usage()
		os.Exit(1)
	}

	evs, errs, err := fswatch.Watch(*dir, *quiescence)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fswatch error: %v\n", err)
		os.Exit(1)
	}

	for {
		if err := attemptBuild(); err == nil {
			startRun()
		}

	events:
		for {
			select {
			case <-evs:
				break events
			case err := <-errs:
				logf("watch error: %v", err)
			}
			time.Sleep(time.Second)
		}
	}
}

func command(prog string, args ...string) *exec.Cmd {
	cmd := exec.Command(prog, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "LIVE=yes")
	return cmd
}

func attemptBuild() error {
	parts := strings.Fields(*build)
	if len(parts) == 0 {
		return nil
	}
	logf("starting build...")
	defer logf("build finished")
	return command(parts[0], parts[1:]...).Run()
}

var (
	running *exec.Cmd
	done    = make(chan struct{})
)

func startRun() {
	parts := strings.Fields(*run)
	if len(parts) == 0 {
		return
	}

	if running != nil {
		logf("killing old run with pid=%d...", running.Process.Pid)
		running.Process.Kill()
		running.Wait()
		running = nil
		<-done
	}

	cmd := command(parts[0], parts[1:]...)
	logf("starting run...")
	if err := cmd.Start(); err == nil {
		logf("run started with pid=%d", cmd.Process.Pid)
		go func() {
			cmd.Wait()
			logf("run finished with pid=%d", cmd.Process.Pid)
			done <- struct{}{}
		}()
		running = cmd
	}
}
