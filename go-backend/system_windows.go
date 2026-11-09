//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ---------------------------------------------------------------- firewall e perfil de rede

// networkHealth resume se o Windows deixa o celular chegar ao LocalDrop.
type networkHealth struct {
	Checked        bool
	PublicNetwork  bool // o Wi-Fi está marcado como rede pública
	Blocked        bool // o firewall não tem permissão de entrada para o LocalDrop
	InterfaceIndex int
}

func (h networkHealth) problem() bool { return h.Checked && (h.PublicNetwork || h.Blocked) }

const networkCheckScript = `$ErrorActionPreference = 'SilentlyContinue'
$ip = $env:LOCALDROP_CHECK_IP; $exe = $env:LOCALDROP_CHECK_EXE
$idx = (Get-NetIPAddress -IPAddress $ip -AddressFamily IPv4).InterfaceIndex
$cat = [string](Get-NetConnectionProfile -InterfaceIndex $idx).NetworkCategory
$name = if ($cat -eq 'DomainAuthenticated') { 'Domain' } elseif ($cat) { $cat } else { 'Public' }
$fw = Get-NetFirewallProfile -Name $name
$enabled = [bool]$fw.Enabled
$defaultAllow = ([string]$fw.DefaultInboundAction) -eq 'Allow'
$allow = $false; $block = $false
$rules = @(Get-NetFirewallApplicationFilter -Program $exe | Get-NetFirewallRule | Where-Object { $_.Direction -eq 'Inbound' -and [string]$_.Enabled -eq 'True' })
foreach ($r in $rules) {
  $p = [string]$r.Profile
  if ($p -eq 'Any' -or $p -match $name) { if ([string]$r.Action -eq 'Block') { $block = $true } else { $allow = $true } }
}
@{ idx = [int]$idx; cat = $cat; enabled = $enabled; defaultAllow = $defaultAllow; allow = $allow; block = $block } | ConvertTo-Json -Compress`

func encodePowerShell(script string) string {
	encoded := utf16.Encode([]rune(script))
	bytes := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		bytes[index*2] = byte(value)
		bytes[index*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

// checkNetworkHealth roda uma consulta rápida do PowerShell, sem janela. Ela só
// lê o estado do Windows; nada é alterado.
func checkNetworkHealth(ip string) networkHealth {
	exe, err := os.Executable()
	if err != nil || ip == "" {
		return networkHealth{}
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(networkCheckScript))
	command.Env = append(os.Environ(), "LOCALDROP_CHECK_IP="+ip, "LOCALDROP_CHECK_EXE="+exe)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	output, err := command.Output()
	if err != nil {
		return networkHealth{}
	}
	var result struct {
		Index        int    `json:"idx"`
		Category     string `json:"cat"`
		Enabled      bool   `json:"enabled"`
		DefaultAllow bool   `json:"defaultAllow"`
		Allow        bool   `json:"allow"`
		Block        bool   `json:"block"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(string(output))), &result) != nil {
		return networkHealth{}
	}
	health := networkHealth{Checked: true, InterfaceIndex: result.Index}
	health.PublicNetwork = strings.EqualFold(result.Category, "Public")
	if result.Enabled && !result.DefaultAllow {
		health.Blocked = result.Block || !result.Allow
	}
	return health
}

// errUserDeclined indica que a pessoa recusou o pedido de permissão do Windows.
var errUserDeclined = errors.New("permissão recusada")

// fixNetworkAccess pede uma única permissão de administrador para liberar o
// LocalDrop no firewall (só redes particulares) e, se preciso, marcar este
// Wi-Fi como rede particular.
func fixNetworkAccess(health networkHealth) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "''") + "'"
	script := `$ErrorActionPreference = 'SilentlyContinue'
$exe = ` + quoted + `
Get-NetFirewallApplicationFilter -Program $exe | Get-NetFirewallRule | Where-Object { $_.Direction -eq 'Inbound' } | Remove-NetFirewallRule
New-NetFirewallRule -DisplayName 'LocalDrop' -Group 'LocalDrop' -Description 'Permite que os seus aparelhos na mesma rede Wi-Fi troquem arquivos com o LocalDrop.' -Direction Inbound -Action Allow -Program $exe -Protocol TCP -Profile Private,Domain | Out-Null
`
	if health.PublicNetwork && health.InterfaceIndex > 0 {
		script += "Set-NetConnectionProfile -InterfaceIndex " + strconv.Itoa(health.InterfaceIndex) + " -NetworkCategory Private\n"
	}
	return runElevatedPowerShell(script)
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func runElevatedPowerShell(script string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString("powershell.exe")
	parameters, _ := windows.UTF16PtrFromString("-NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + encodePowerShell(script))
	info := shellExecuteInfo{
		fMask:  0x00000040 | 0x00000100, // SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC
		lpVerb: verb, lpFile: file, lpParameters: parameters, nShow: 0,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	ok, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errno, isErrno := callErr.(syscall.Errno); isErrno && errno == windows.ERROR_CANCELLED {
			return errUserDeclined
		}
		return callErr
	}
	if info.hProcess != 0 {
		_, _ = windows.WaitForSingleObject(info.hProcess, 120_000)
		_ = windows.CloseHandle(info.hProcess)
	}
	return nil
}

// ---------------------------------------------------------------- abrir com o Windows

const autostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `" --tray`, nil
}

func autostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue("LocalDrop")
	return err == nil
}

func setAutostart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		if err := key.DeleteValue("LocalDrop"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	command, err := autostartCommand()
	if err != nil {
		return err
	}
	return key.SetStringValue("LocalDrop", command)
}

// refreshAutostart corrige o caminho gravado quando o programa mudou de pasta.
func refreshAutostart() {
	if !autostartEnabled() {
		return
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	current, _, _ := key.GetStringValue("LocalDrop")
	if command, err := autostartCommand(); err == nil && !strings.EqualFold(current, command) {
		_ = key.SetStringValue("LocalDrop", command)
	}
}

// ---------------------------------------------------------------- memória

var procSetProcessWorkingSetSizeEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessWorkingSetSizeEx")

// trimMemory devolve ao Windows a memória que o LocalDrop não está usando. É
// chamado ao ir para a bandeja, quando nada precisa ficar desenhado.
func trimMemory() {
	debug.FreeOSMemory()
	process, _ := windows.GetCurrentProcess()
	minusOne := ^uintptr(0)
	_, _, _ = procSetProcessWorkingSetSizeEx.Call(uintptr(process), minusOne, minusOne, 0)
}
