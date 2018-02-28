// +build !linux

package main

import "syscall"

func setProcAttr(attr *syscall.SysProcAttr) {}
