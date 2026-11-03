package run

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"unicode/utf16"
)

// shellArgv builds the command line that runs cmd. shell is the job's
// shell field, such as "bash -c"; the command is appended as the last
// argument. Empty means sh -c on Unix and PowerShell on Windows.
func shellArgv(shell, cmd, goos string) []string {
	if shell != "" {
		return append(strings.Fields(shell), cmd)
	}
	if goos != "windows" {
		return []string{"sh", "-c", cmd}
	}
	ps := "pwsh"
	extra := []string{}
	if _, err := exec.LookPath("pwsh"); err != nil {
		ps = "powershell.exe"
		extra = []string{"-ExecutionPolicy", "Bypass"}
	}
	// Without these, a failing native command does not stop the script and
	// only the last statement decides the exit code.
	script := "$ErrorActionPreference = 'Stop'; $PSNativeCommandUseErrorActionPreference = $true\n" + cmd
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	argv := append([]string{ps, "-NoLogo", "-NoProfile", "-NonInteractive"}, extra...)
	return append(argv, "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
}
