package main

import (
    "io"
    "net"
    "net/textproto"
    "strings"
    "sync"
    "time"
)

type TextprotoServerInterface struct {
    handler HandlerFn
}

// Listen for HTTP on `addr`, let `handler` handle requests, signal
// `waitGroup` when done
func (self *TextprotoServerInterface) Listen(addr string, handler HandlerFn, waitGroup *sync.WaitGroup) {
    self.handler = handler
    defer waitGroup.Done()

    tcpAddr, resolveErr := net.ResolveTCPAddr("tcp", addr)
    if resolveErr != nil {
        errLog.Printf("net.ResolveTCPAddr err=%v\n", resolveErr)
        return
    }

    tcpListener, listenErr := net.ListenTCP("tcp", tcpAddr)
    if listenErr != nil {
        errLog.Printf("net.ListenTCP err=%v\n", listenErr)
        return
    }

    for {
        if tcpConn, acceptErr := tcpListener.AcceptTCP(); acceptErr != nil {
            errLog.Printf("tcpListener.AcceptTCP err=%v\n", acceptErr)
            return
        } else {
            go self.handleConn(tcpConn)
        }
    }
}

// Handle a text conn. Read requests and write responses.
func (self *TextprotoServerInterface) handleConn(tcpConn *net.TCPConn) {
    var err error
    textConn := textproto.NewConn(tcpConn)

    // For-once loop
    for {
        // Read line from socket
        requestLine := ""
        if requestLine, err = textConn.ReadLine(); err != nil {
            if err != io.EOF {
                errLog.Printf("textConn.ReadLine err=%v\n", err)
            }
            break
        }

        // Split line in to args by whitespace. Note this isn't perfect. No
        // quoted tokenizing, etc.
        scriptArgs := strings.Fields(requestLine)
        if len(scriptArgs) < 1 {
            break
        }

        // Expect each arg in the format of '-*key=val'. Again, not perfect.
        scriptParams := make(map[string]string)
        for _, scriptArg := range scriptArgs[1:] {
            scriptKeyVal := strings.SplitN(scriptArg, "=", 2)
            if len(scriptKeyVal) == 2 {
                scriptParams[strings.TrimLeft(scriptKeyVal[0], "-")] = scriptKeyVal[1]
            }
        }

        // Pass to server code for handling
        resp := self.handler(&Request{
            ScriptName:      scriptArgs[0],
            Params:          scriptParams,
            ServerInterface: self,
            Ts:              time.Now().Unix(),
            RemoteAddr:      tcpConn.RemoteAddr().String(),
        })

        // Write response
        self.writeResponse(textConn, resp)
        break
    }

    // Close conn
    if err = textConn.Close(); err != nil {
        errLog.Printf("textConn.Close err=%v\n", err)
    }
}

// Try to write `resp` to `textConn`
func (self *TextprotoServerInterface) writeResponse(textConn *textproto.Conn, resp *Response) {
    var err error
    code := "OK"
    if resp.Error != nil || resp.StatusCode >= 400 {
        code = "ERR"
    }
    err = textConn.Writer.PrintfLine("%s %d", code, resp.StatusCode)
    if err == nil {
        if resp.Body != "" {
            err = textConn.Writer.PrintfLine("%s", resp.Body)
        } else if resp.Error != nil {
            err = textConn.Writer.PrintfLine("%s", resp.Error.Error())
        } else {
            statii := make([]string, 0)
            for _, status := range resp.RunStatii {
                statii = append(statii, status.String())
            }
            err = textConn.Writer.PrintfLine("%s", strings.Join(statii, ""))
        }
    }
    if err != nil {
        errLog.Printf("textConn.Writer.PrintfLine err=%v\n", err)
    }
}
