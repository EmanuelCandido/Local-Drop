package main

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

// O identificador do celular mora no armazenamento do navegador, que é separado
// por endereço: quando o IP ou a porta do computador muda, ou o QR abre em outro
// navegador, o mesmo celular chega com um identificador novo. A chave de
// hardware (derivada do endereço físico na rede local) permite reconhecê-lo.

// resolveHardwareKey é variável para os testes poderem simular aparelhos.
var resolveHardwareKey = func(r *http.Request) string {
	ip := net.ParseIP(remoteIP(r))
	if ip == nil || ip.IsLoopback() {
		return ""
	}
	mac := hardwareAddress(ip)
	if mac == "" {
		return ""
	}
	return hashValue("localdrop-hw:" + mac)[:32]
}

// isAutomaticDeviceName diz se o nome é o sugerido pelo próprio LocalDrop
// ("Meu celular", "Meu PC 2"...), e não um escolhido pela pessoa.
func isAutomaticDeviceName(name string) bool {
	base := strings.TrimRight(strings.TrimSpace(name), "0123456789 ")
	return strings.EqualFold(base, "Meu celular") || strings.EqualFold(base, "Meu PC")
}

// adoptHardwareLocked grava a chave de hardware no aparelho e absorve os
// registros antigos do mesmo aparelho físico: o histórico passa para o registro
// atual e os antigos saem da lista. Devolve os identificadores removidos e o
// nome personalizado herdado ("" se não houver). Registros que estão online
// agora são preservados (outro navegador aberto no mesmo aparelho).
func (s *Server) adoptHardwareLocked(device *DeviceRecord, key string) (removed []string, inheritedName string) {
	if key == "" || device.Host {
		return nil, ""
	}
	device.HardwareKey = key
	previous := make([]*DeviceRecord, 0, 2)
	for _, other := range s.state.Devices {
		if other.DeviceID == device.DeviceID || other.Host || other.Revoked || other.HardwareKey != key {
			continue
		}
		if s.hub.IsOnline(other.DeviceID) {
			continue
		}
		previous = append(previous, other)
	}
	sort.Slice(previous, func(i, j int) bool { return previous[i].LastSeenAt.After(previous[j].LastSeenAt) })
	for _, other := range previous {
		if inheritedName == "" && !isAutomaticDeviceName(other.DisplayName) {
			inheritedName = other.DisplayName
		}
		if other.CreatedAt.Before(device.CreatedAt) {
			device.CreatedAt = other.CreatedAt
		}
		for _, transfer := range s.state.Transfers {
			if transfer.SenderID == other.DeviceID {
				transfer.SenderID = device.DeviceID
			}
			if transfer.ReceiverID == other.DeviceID {
				transfer.ReceiverID = device.DeviceID
			}
			for index, hidden := range transfer.HiddenFor {
				if hidden == other.DeviceID {
					transfer.HiddenFor[index] = device.DeviceID
				}
			}
		}
		delete(s.state.Devices, other.DeviceID)
		removed = append(removed, other.DeviceID)
	}
	return removed, inheritedName
}

// pairedNameLocked escolhe o nome do aparelho recém-conectado: mantém o nome
// personalizado de antes quando o aparelho voltou só com o nome automático.
func (s *Server) pairedNameLocked(requested, inherited, deviceID string) string {
	if inherited != "" && isAutomaticDeviceName(requested) {
		requested = inherited
	}
	return s.uniqueDeviceNameLocked(requested, deviceID)
}

// announceAbsorbed avisa as telas abertas que os registros antigos sumiram.
func (s *Server) announceAbsorbed(removed []string) {
	for _, id := range removed {
		s.hub.Disconnect(id)
		s.hub.Broadcast("DEVICE_REVOKED", map[string]string{"deviceId": id})
	}
}
