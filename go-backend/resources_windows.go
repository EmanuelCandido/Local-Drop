//go:build windows

package main

// rsrc assigns resource 1 to the manifest and resource 2 to the first icon
// group. The generated .syso makes the icon available to both Explorer and
// the native Walk window.
//
//go:generate go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest localdrop.exe.manifest -ico assets/LocalDrop.ico -o rsrc_windows_amd64.syso
const appIconResourceID = 2
