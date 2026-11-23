//go:build windows

package main

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSendARP = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("SendARP")

// hardwareAddress devolve o endereço físico (MAC) de um aparelho da rede local,
// ou "" quando ele não está na mesma rede ou não respondeu.
func hardwareAddress(ip net.IP) string {
	ip4 := ip.To4()
	if ip4 == nil {
		return ""
	}
	var mac [8]byte
	size := uint32(len(mac))
	result, _, _ := procSendARP.Call(
		uintptr(binary.LittleEndian.Uint32(ip4)), 0,
		uintptr(unsafe.Pointer(&mac[0])), uintptr(unsafe.Pointer(&size)))
	if result != 0 || size == 0 || size > uint32(len(mac)) {
		return ""
	}
	empty := true
	for _, value := range mac[:size] {
		if value != 0 {
			empty = false
			break
		}
	}
	if empty {
		return ""
	}
	return hex.EncodeToString(mac[:size])
}
