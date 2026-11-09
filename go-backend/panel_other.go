//go:build !windows

package main

func showDesktopPanel(_ *Server, _ string) bool { return false }
