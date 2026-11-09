//go:build !windows

package main

import "net"

func localAdapterAddresses() []adapterAddress {
	interfaces, _ := net.Interfaces()
	result := make([]adapterAddress, 0, 4)
	for _, current := range interfaces {
		if current.Flags&net.FlagUp == 0 || current.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := current.Addrs()
		if err != nil {
			continue
		}
		for _, candidate := range addresses {
			ip, _, err := net.ParseCIDR(candidate.String())
			if err == nil && ip.To4() != nil {
				result = append(result, adapterAddress{name: current.Name, ip: ip.To4()})
			}
		}
	}
	return result
}
