package main

import (
    "bytes"
    "runtime"
)

// Port of php_escape_shell_arg
// See https://github.com/php/php-src/blob/master/ext/standard/exec.c
func escapeShellArg(arg string) string {
    var quotedArg bytes.Buffer
    quotedArg.Grow(len(arg))

    if runtime.GOOS == "windows" {
        quotedArg.WriteString(`"`)
    } else {
        quotedArg.WriteString(`'`)
    }

    for _, runeVal := range arg {
        if runtime.GOOS == "windows" {
            if runeVal == '"' || runeVal == '%' {
                quotedArg.WriteRune(' ')
                continue
            }
        } else {
            if runeVal == '\'' {
                quotedArg.WriteString(`'\'`)
            }
        }
        quotedArg.WriteRune(runeVal)
    }
    if runtime.GOOS == "windows" {
        quotedArg.WriteString(`"`)
    } else {
        quotedArg.WriteString(`'`)
    }

    return quotedArg.String()
}
