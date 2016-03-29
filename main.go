package main

import (
    "flag"
    "fmt"
    "log"
    "os"
    "sync"
)

const (
    VERSION = "1.6"
)

var (
    config   Config
    infoLog  *log.Logger
    errLog   *log.Logger
    infoFile *os.File
    errFile  *os.File
)

type Config struct {
    ScriptDir     string
    JsonAddr      string
    TextprotoAddr string
    InfoLogPath   string
    ErrLogPath    string
}

// A `HandlerFn` takes a `Request` and returns a `Response`
type HandlerFn func(*Request) *Response

// A `ServerInterface` listens for connections at `addr`, forms a `Request`
// based on client input, passes the `Request` to `handler` and writes the
// resulting `Response` to the client. It should also signal `waitGroup` when
// `Listen` exits.
type ServerInterface interface {
    Listen(addr string, handler HandlerFn, waitGroup *sync.WaitGroup)
}

// Program entry point
func main() {
    var waitGroup sync.WaitGroup
    var printVersion bool

    flag.StringVar(&config.ScriptDir, "d", "/etc/gobashd.d/", "Bash script directory")
    flag.StringVar(&config.JsonAddr, "j", ":4488", "If not-empty, listen for JSON request at this address")
    flag.StringVar(&config.TextprotoAddr, "t", ":4489", "If not-empty, listen for textproto request at this address")
    flag.StringVar(&config.InfoLogPath, "i", "", "If not-empty, write info log here instead of stdout")
    flag.StringVar(&config.ErrLogPath, "e", "", "If not-empty, write error log here instead of stderr")
    flag.BoolVar(&printVersion, "v", false, "Print version and exit")
    flag.Parse()

    if printVersion {
        fmt.Printf("gobashd version=%s\n", VERSION)
        return
    }

    infoLog = log.New(os.Stdout, "[I] ", log.LstdFlags)
    errLog = log.New(os.Stderr, "[E] ", log.LstdFlags)

    if err := os.Chdir(config.ScriptDir); err != nil {
        errLog.Fatalf("os.Chdir failed; err=%v\n", err)
    }

    server := newServer()
    server.ReopenLogs()
    server.LoadScripts(".")

    if config.JsonAddr != "" {
        waitGroup.Add(1)
        go new(JsonServerInterface).Listen(config.JsonAddr, server.Handle, &waitGroup)
    }
    if config.TextprotoAddr != "" {
        waitGroup.Add(1)
        go new(TextprotoServerInterface).Listen(config.TextprotoAddr, server.Handle, &waitGroup)
    }

    server.reloadOnHup()

    waitGroup.Wait()
}
