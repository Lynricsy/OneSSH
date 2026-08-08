package webapi

import "golang.org/x/crypto/ssh"

var sshTerminalModes = ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
