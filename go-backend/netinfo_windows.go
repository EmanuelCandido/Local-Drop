//go:build windows

package main

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// localAdapterAddresses lê as placas pelo GetAdaptersAddresses, que informa o
// tipo físico, a descrição ("TAP-Windows", "Hyper-V"...) e se há gateway.
func localAdapterAddresses() []adapterAddress {
	size := uint32(16 * 1024)
	var buffer []byte
	for attempt := 0; attempt < 3; attempt++ {
		buffer = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_INET,
			windows.GAA_FLAG_INCLUDE_GATEWAYS|windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return fallbackAdapterAddresses()
		}
		if attempt == 2 {
			return fallbackAdapterAddresses()
		}
	}
	result := make([]adapterAddress, 0, 4)
	for adapter := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0])); adapter != nil; adapter = adapter.Next {
		if adapter.OperStatus != windows.IfOperStatusUp || adapter.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		name := windows.UTF16PtrToString(adapter.FriendlyName)
		description := windows.UTF16PtrToString(adapter.Description)
		for unicast := adapter.FirstUnicastAddress; unicast != nil; unicast = unicast.Next {
			ip := unicast.Address.IP()
			if ip == nil || ip.To4() == nil {
				continue
			}
			result = append(result, adapterAddress{
				name: name, description: description, ip: ip.To4(),
				wireless:   adapter.IfType == windows.IF_TYPE_IEEE80211,
				wired:      adapter.IfType == windows.IF_TYPE_ETHERNET_CSMACD,
				hasGateway: adapter.FirstGatewayAddress != nil,
			})
		}
	}
	return result
}

func fallbackAdapterAddresses() []adapterAddress {
	return interfaceAddresses()
}

func interfaceAddresses() []adapterAddress {
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
