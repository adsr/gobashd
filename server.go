package main

import (
    "bytes"
    "errors"
    "fmt"
    "io/ioutil"
    "os"
    "os/signal"
    "strconv"
    "sync"
    "syscall"
    "time"
)

type Server struct {
    Scripts        map[string]*Script
    ScriptRuns     []*ScriptRun
    ScriptsLock    sync.Mutex
    ScriptRunsLock sync.Mutex
}

type Request struct {
    ScriptName string
    Params     map[string]string
    ServerInterface
    Ts         int64
    RemoteAddr string
}

type Response struct {
    StatusCode int
    Body       string
    Error      error
    ErrorStr   string
    RunStatii  []*ScriptRunStatus
}

func newServer() *Server {
    server := new(Server)
    server.Scripts = make(map[string]*Script)
    server.ScriptRuns = make([]*ScriptRun, 0)
    return server
}

// Load bash scripts at `ScriptDir`
func (self *Server) LoadScripts(scriptDir string) {
    self.ScriptsLock.Lock()
    defer self.ScriptsLock.Unlock()
    self.Scripts = make(map[string]*Script) // Reset Scripts map
    fileInfos, err := ioutil.ReadDir(scriptDir)
    if err != nil {
        errLog.Printf("ioutil.ReadDir err=%v\n", err)
        return
    }
    for _, fileInfo := range fileInfos {
        if !fileInfo.Mode().IsRegular() ||
            fileInfo.Mode().Perm()&0500 != 0500 ||
            uint32(os.Getuid()) != fileInfo.Sys().(*syscall.Stat_t).Uid {
            // Not a regular file
            // or not r-x by user
            // or not owned by user
            continue
        }
        scriptPath := fmt.Sprintf("%s/%s", scriptDir, fileInfo.Name())
        fileBytes, readErr := ioutil.ReadFile(scriptPath)
        if readErr != nil {
            errLog.Printf("ioutil.ReadFile err=%v\n", readErr)
            continue
        }
        script, scriptErr := newScript(scriptPath, fileBytes)
        if scriptErr != nil {
            errLog.Printf("newScript err=%v\n", scriptErr)
            continue
        }
        self.Scripts[script.Name] = script
        infoLog.Printf("Loaded script %s\n", script.Name)
    }
}

// Handle request `req`. Return a `Response`.
func (self *Server) Handle(req *Request) *Response {
    var script *Script
    resp := &Response{}

    // Handle built-in commands
    if req.ScriptName == "help" {
        resp.StatusCode = 200
        resp.Body = self.getScriptHelp()
        return resp
    } else if req.ScriptName == "status" {
        if statii, statusErr := self.getRunStatii(req.Params["id"]); statusErr != nil {
            resp.StatusCode = 400
            resp.Error = statusErr
            resp.ErrorStr = statusErr.Error()
        } else {
            resp.StatusCode = 200
            resp.RunStatii = statii
        }
        return resp
    } else if req.ScriptName == "view" {
        if bashScript, viewErr := self.getBashScript(req.Params["id"]); viewErr != nil {
            resp.StatusCode = 400
            resp.Error = viewErr
            resp.ErrorStr = viewErr.Error()
        } else {
            resp.StatusCode = 200
            resp.Body = bashScript
        }
        return resp
    } else if req.ScriptName == "kill" {
        if killErr := self.killRun(req.Params["id"]); killErr != nil {
            resp.StatusCode = 400
            resp.Error = killErr
            resp.ErrorStr = killErr.Error()
        } else {
            resp.StatusCode = 200
            resp.Body = fmt.Sprintf("Sent kill to ScriptRun %s", req.Params["id"])
        }
        return resp
    } else if req.ScriptName == "version" {
        resp.StatusCode = 200
        resp.Body = VERSION
        return resp
    } else if req.ScriptName == "purge" {
        resp.StatusCode = 200
        resp.Body = fmt.Sprintf("Purged %d ScriptRuns from status history", self.purgeScriptRuns(0))
        return resp
    }

    // Handle script commands
    func() {
        self.ScriptsLock.Lock()
        defer self.ScriptsLock.Unlock()
        script = self.Scripts[req.ScriptName]
    }()
    if script == nil {
        resp.StatusCode = 404
        resp.Error = errors.New("Script or command does not exist")
        resp.ErrorStr = "Script or command does not exist"
        return resp
    }
    scriptRun, err := self.makeScriptRun(script, req)
    if err != nil {
        resp.StatusCode = 400
        resp.Error = err
        resp.ErrorStr = err.Error()
        return resp
    }
    go scriptRun.run()
    resp.StatusCode = 200
    resp.RunStatii = []*ScriptRunStatus{scriptRun.Status()}
    return resp
}

// Given a `Script` and a `Request`, return a `ScriptRun`.
func (self *Server) makeScriptRun(script *Script, req *Request) (*ScriptRun, error) {
    params, err := script.normalizeParams(req.Params)
    if err != nil {
        return nil, err
    }
    uuid, uuidErr := getUuid()
    if uuidErr != nil {
        return nil, uuidErr
    }
    scriptRun := &ScriptRun{
        Script:       script,
        Request:      req,
        Id:           uuid,
        LogId:        req.Params["logid"],
        Params:       params,
        OutputLocks:  make([]sync.Mutex, len(script.OutputDefs)),
        TimeoutSetTs: time.Now().Unix(),
        TimeoutSet:   make(chan bool),
    }
    for _ = range scriptRun.OutputLocks {
        scriptRun.Outputs = append(scriptRun.Outputs, &bytes.Buffer{})
    }
    var scriptBuf bytes.Buffer
    if tplErr := script.Template.Execute(&scriptBuf, scriptRun.Params); tplErr != nil {
        return nil, tplErr
    } else {
        scriptRun.BashScript = scriptBuf.String()
    }
    func() {
        self.ScriptRunsLock.Lock()
        defer self.ScriptRunsLock.Unlock()
        self.ScriptRuns = append(self.ScriptRuns, scriptRun)
    }()
    return scriptRun, nil
}

// Remove items from `ScriptRuns` older than `maxAge` seconds old.
func (self *Server) purgeScriptRuns(maxAge uint64) int {
    self.ScriptRunsLock.Lock()
    defer self.ScriptRunsLock.Unlock()
    newScriptRuns := make([]*ScriptRun, 0, len(self.ScriptRuns))
    now := time.Now().Unix()
    for _, scriptRun := range self.ScriptRuns {
        if !scriptRun.Finished || now-scriptRun.FinishTs <= int64(maxAge) {
            newScriptRuns = append(newScriptRuns, scriptRun)
        }
    }
    numPurged := len(self.ScriptRuns) - len(newScriptRuns)
    self.ScriptRuns = newScriptRuns
    return numPurged
}

// Get script help
func (self *Server) getScriptHelp() string {
    self.ScriptsLock.Lock()
    defer self.ScriptsLock.Unlock()
    var helpBuf bytes.Buffer
    helpBuf.WriteString("\n")
    for scriptName, script := range self.Scripts {
        helpBuf.WriteString(scriptName)
        helpBuf.WriteString("\n")
        helpBuf.WriteString(strconv.FormatInt(script.ParsedTs, 10))
        helpBuf.WriteString("\n")
        helpBuf.WriteString(script.Help)
        helpBuf.WriteString("\n")
    }
    return helpBuf.String()
}

// Return the `ScriptRunStatus` of one or more `ScriptRun`
func (self *Server) getRunStatii(id string) ([]*ScriptRunStatus, error) {
    self.ScriptRunsLock.Lock()
    defer self.ScriptRunsLock.Unlock()
    statii := make([]*ScriptRunStatus, 0)
    if id != "" {
        scriptRun := self.getRunById(id)
        if scriptRun == nil {
            return nil, errors.New(fmt.Sprintf("ScriptRun with id %s does not exist", id))
        }
        statii = append(statii, scriptRun.Status())
    } else {
        for _, scriptRun := range self.ScriptRuns {
            statii = append(statii, scriptRun.Status())
        }
    }
    return statii, nil
}

// Return the BashScript property of a `ScriptRun`
func (self *Server) getBashScript(id string) (string, error) {
    self.ScriptRunsLock.Lock()
    defer self.ScriptRunsLock.Unlock()
    scriptRun := self.getRunById(id)
    if scriptRun == nil {
        return "", errors.New(fmt.Sprintf("ScriptRun with id %s does not exist", id))
    }
    return scriptRun.BashScript, nil
}

// Kill a script
func (self *Server) killRun(id string) error {
    self.ScriptRunsLock.Lock()
    defer self.ScriptRunsLock.Unlock()
    scriptRun := self.getRunById(id)
    if scriptRun == nil {
        return errors.New(fmt.Sprintf("ScriptRun with id %s does not exist", id))
    }
    return scriptRun.kill()
}

// Reload scripts on SIGHUP
func (self *Server) reloadScriptsOnHup() {
    c := make(chan os.Signal, 1)
    signal.Notify(c, syscall.SIGHUP)
    go func() {
        for _ = range c {
            infoLog.Printf("Caught SIGHUP, reloading scripts...")
            self.LoadScripts(".")
        }
    }()
}

// Given an `Id`, return a `ScriptRun`. Return nil if no such `ScriptRun`
// exists. This function assumes the `ScriptRunsLock` lock is is already
// acquired.
func (self *Server) getRunById(id string) *ScriptRun {
    for _, scriptRun := range self.ScriptRuns {
        if scriptRun.Id == id {
            return scriptRun
        }
    }
    return nil
}
