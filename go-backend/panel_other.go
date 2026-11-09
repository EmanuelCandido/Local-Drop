//go:build !windows

package main

func showDesktopPanel(_ *Server, _ bool) bool { return false }
