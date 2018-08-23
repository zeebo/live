package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zeebo/errs"
)

var (
	dir        = flag.String("dir", "", "watched directory")
	build      = flag.String("build", "", "build command")
	run        = flag.String("run", "", "run command")
	quiescence = flag.Duration("quiescence", 100*time.Millisecond,
		"quiescence period after a change before triggering build")
)

func logf(format string, args ...interface{}) {
	fmt.Printf("--- %s\n", fmt.Sprintf(format, args...))
}

func main() {
	_, err := exec.LookPath("fswatch")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: fswatch must be available")
		os.Exit(1)
	}

	flag.Parse()
	if *build == "" && *run == "" {
		fmt.Fprintln(os.Stderr, "error: must specify run and/or build.")
		flag.Usage()
		os.Exit(1)
	}

	for {
		if err := attemptBuild(); err == nil {
			startRun()
		}

		for {
			if err := notify(); err == nil {
				break
			}
			time.Sleep(time.Second)
		}
	}
}

func notify() error {
	cmd := exec.Command("fswatch", *dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errs.Wrap(err)
	}
	if err := cmd.Start(); err != nil {
		return errs.Wrap(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()

	errors := make(chan error, 1)
	change := make(chan struct{})
	timer := (*time.Timer)(nil)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "/_") {
				continue
			}
			change <- struct{}{}
		}
		errors <- errs.Wrap(scanner.Err())
	}()

	for {
		var timer_ch <-chan time.Time
		if timer != nil {
			timer_ch = timer.C
		}

		select {
		case err := <-errors:
			if timer != nil {
				timer.Stop()
			}
			logf("fswatch error: %v", err)
			return err

		case <-timer_ch:
			logf("quiesence reached")
			return nil

		case <-change:
			if timer != nil {
				timer.Stop()
			} else {
				logf("change noticed")
			}
			timer = time.NewTimer(*quiescence)
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
