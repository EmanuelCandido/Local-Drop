//go:build !windows

package main

import "net"

func hardwareAddress(net.IP) string { return "" }
