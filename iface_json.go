package main

import (
    "encoding/json"
    "net/http"
    "strings"
    "sync"
    "time"
)

type JsonServerInterface struct {
    handler HandlerFn
}

// Listen for HTTP on `addr`, let `handler` handle requests, signal
// `waitGroup` when done
func (self *JsonServerInterface) Listen(addr string, handler HandlerFn, waitGroup *sync.WaitGroup) {
    self.handler = handler
    defer waitGroup.Done()
    err := http.ListenAndServe(addr, self)
    if err != nil {
        errLog.Printf("http.ListenAndServe err=%v\n", err)
    }
}

// Handle a JSON request and write response
func (self *JsonServerInterface) ServeHTTP(httpResp http.ResponseWriter, httpReq *http.Request) {
    httpReq.ParseForm()
    params := make(map[string]string)
    for key, vals := range httpReq.Form {
        if len(vals) > 1 {
            params[key] = strings.Join(vals, ",")
        } else {
            params[key] = vals[0]
        }
    }
    resp := self.handler(&Request{
        ScriptName:      strings.Trim(httpReq.RequestURI, "/"),
        Params:          params,
        ServerInterface: self,
        Ts:              time.Now().Unix(),
        RemoteAddr:      httpReq.RemoteAddr,
    })
    httpResp.Header().Set("Content-Type", "application/json")
    if jsonBytes, err := json.MarshalIndent(resp, "", "    "); err != nil {
        httpResp.WriteHeader(http.StatusInternalServerError)
        errLog.Printf("json.MarshalIndent err=%v\n", err)
    } else {
        httpResp.WriteHeader(resp.StatusCode)
        if _, err = httpResp.Write(jsonBytes); err != nil {
            errLog.Printf("httpResp.Write err=%v\n", err)
        }
    }
}
