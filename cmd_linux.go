package main

import "syscall"

func setProcAttr(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}
