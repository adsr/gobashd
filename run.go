package main

import (
    "bufio"
    "bytes"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "sync"
    "syscall"
    "time"
)

type ScriptRun struct {
    *Script
    *Request
    Id           string
    LogId        string
    Cmd          *exec.Cmd
    ExitCode     int
    BashScript   string
    TimeoutSet   chan bool `json:"-"`
    TimeoutSetTs int64
    Timeout      uint64
    Params       map[string]interface{}
    Outputs      []*bytes.Buffer
    OutputLocks  []sync.Mutex
    ExtraPipes   []io.Closer `json:"-"`
    StartTs      int64
    FinishTs     int64
    Finished     bool
}

type ScriptRunStatus struct {
    ScriptName   string
    Id           string
    ScriptTs     int64
    Params       map[string]string
    Outputs      map[string]string
    TimeoutSetTs int64
    StartTs      int64
    FinishTs     int64
    Finished     bool
    ExitCode     int
}

// Run a `ScriptRun`. This invokes the underlying bash script and launches
// go routines to observe output and handle timeouts.
func (self *ScriptRun) run() {
    // Make command
    self.makeCommand()

    // Make pipes
    pipeErr := self.makePipes()
    if pipeErr != nil {
        // Failed to make pipes
        errLog.Printf("makePipes failed pipeErr=%v\n", pipeErr)
    } else {
        // Run and check exit code
        runErr := self.runWithTimeout()
        if exitErr, isExitErr := runErr.(*exec.ExitError); isExitErr {
            if status, isStatus := exitErr.Sys().(syscall.WaitStatus); isStatus {
                self.ExitCode = status.ExitStatus()
            } else {
                errLog.Printf("Unable to get ExitStatus\n")
                self.ExitCode = 1
            }
        }
    }

    // Mark finished
    self.FinishTs = time.Now().Unix()
    self.Finished = true
    self.logInfo("Finished ExitCode=%d\n", self.ExitCode)
}

// Make command
func (self *ScriptRun) makeCommand() {
    self.Cmd = exec.Command("bash", "-c", self.BashScript) // TODO bash args
}

// Make and observe pipes
func (self *ScriptRun) makePipes() error {
    var pipeErr error
    var readPipe io.ReadCloser
    var writePipe io.WriteCloser

    self.Cmd.ExtraFiles = make([]*os.File, 2+len(self.Outputs)) // _clear, _timeout (2) + output vars
    for fd := 1; fd <= 4+len(self.Outputs); fd++ {              // stdout, stderr, _clear, _timeout (4) + output vars
        if fd == 1 {
            // stdout
            readPipe, pipeErr = self.Cmd.StdoutPipe()
            if pipeErr != nil {
                return pipeErr
            }
        } else if fd == 2 {
            // stderr
            readPipe, pipeErr = self.Cmd.StderrPipe()
            if pipeErr != nil {
                return pipeErr
            }
        } else {
            // _clear, _timeout, or output var
            readPipe, writePipe, pipeErr = os.Pipe()
            if pipeErr != nil {
                return pipeErr
            }
            self.Cmd.ExtraFiles[fd-3] = writePipe.(*os.File)
            self.ExtraPipes = append(self.ExtraPipes, readPipe)
            self.ExtraPipes = append(self.ExtraPipes, writePipe)
        }
        go self.readOutput(fd, readPipe) // Read pipe in go routine
    }
    return nil
}

// Run command until it finishes or times out
func (self *ScriptRun) runWithTimeout() error {
    var runErr error

    // Signal `done` chan when finished
    done := make(chan bool)
    go func() {
        self.StartTs = time.Now().Unix()
        self.Cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Make separate process group
        runErr = self.Cmd.Run()                                    // This runs the command
        done <- true
    }()

    // Enter timeout loop
    killed := false
waitLoop:
    for {
        waitSecs := self.Timeout
        if waitSecs == 0 {
            waitSecs = 3600
        }
        select {
        case <-done:
            // Finished!
            break waitLoop
        case <-self.TimeoutSet:
            // Timeout was updated
            self.logInfo("Timeout updated to %d\n", self.Timeout)
            continue waitLoop
        case <-time.After(time.Duration(waitSecs) * time.Second):
            // Maybe timed out
            if !killed && self.Timeout > 0 && time.Now().Unix()-self.TimeoutSetTs >= int64(self.Timeout) {
                // Timed out!
                self.logInfo("Timed out; sending kill signal\n")
                self.kill()
                killed = true
            }
        }
    }

    // Close ExtraPipes
    for _, extraPipe := range self.ExtraPipes {
        if pipeErr := extraPipe.Close(); pipeErr != nil {
            self.logErr("extraPipe.Close failed pipeErr=%v\n", pipeErr);
        }
    }

    // Return runErr
    return runErr
}

// Kill a script run and all child processes
func (self *ScriptRun) kill() error {
    if self.Cmd == nil {
        return errors.New("Cmd is nil")
    } else if self.Cmd.Process == nil {
        return errors.New("Cmd.Process is nil")
    }
    pgid, err := syscall.Getpgid(self.Cmd.Process.Pid)
    if err != nil {
        return err
    }
    return syscall.Kill(-pgid, syscall.SIGKILL)
}

// Read output from readPipe. This can be stdout, stderr, _clear or _timeout
// input, or setting an output var.
func (self *ScriptRun) readOutput(fd int, readPipe io.ReadCloser) {
    reader := bufio.NewReader(readPipe)
    for {
        line, err := reader.ReadString('\n')
        if err != nil {
            break
        }
        if fd == 1 {
            // stdout
            self.logInfo("%s", line)
        } else if fd == 2 {
            // stderr
            self.logErr("%s", line)
        } else if fd == 3 {
            // _clear
            if outputIdx := self.Script.getOutputIdxByName(strings.TrimSpace(line)); outputIdx > 0 {
                self.Outputs[outputIdx].Reset()
            } else {
                self.logErr("Failed to _clear %s; no such output\n", strings.TrimSpace(line))
            }
        } else if fd == 4 {
            // _timeout
            if timeoutVal, tErr := strconv.ParseUint(strings.TrimSpace(line), 10, 64); tErr == nil {
                self.Timeout = timeoutVal
                self.TimeoutSetTs = time.Now().Unix()
                self.TimeoutSet <- true
            } else {
                self.logErr("Failed to set _timeout to %s\n", strings.TrimSpace(line))
            }
        } else {
            // output vars
            outputIdx := fd - 5
            outputDef := self.Script.OutputDefs[outputIdx]
            trimLine := strings.TrimSpace(line)
            func() {
                self.OutputLocks[outputIdx].Lock()
                defer self.OutputLocks[outputIdx].Unlock()
                outputBuf := self.Outputs[outputIdx]
                if outputDef.Type == "w" {
                    outputBuf.Reset()
                    line = trimLine
                }
                outputBuf.WriteString(line)
            }()
            self.logInfo("%s: %s\n", outputDef.Name, trimLine)
        }
    }
}

// Log info with `ScriptRun` context
func (self *ScriptRun) logInfo(f string, v ...interface{}) {
    f = fmt.Sprintf("[%s] [%s] [%s] %s", self.Script.Name, self.LogId, self.Id, f)
    infoLog.Printf(f, v...)
}

// Log error with `ScriptRun` context
func (self *ScriptRun) logErr(f string, v ...interface{}) {
    f = fmt.Sprintf("[%s] [%s] [%s] %s", self.Script.Name, self.LogId, self.Id, f)
    errLog.Printf(f, v...)
}

// Get status as string
func (self *ScriptRun) Status() *ScriptRunStatus {
    effectiveParams := make(map[string]string)
    for k, v := range self.Params {
        effectiveParams[k] = fmt.Sprintf("%v", v)
    }
    status := &ScriptRunStatus{
        ScriptName:   self.Script.Name,
        ScriptTs:     self.Script.ParsedTs,
        Id:           self.Id,
        Params:       effectiveParams,
        TimeoutSetTs: self.TimeoutSetTs,
        StartTs:      self.StartTs,
        FinishTs:     self.FinishTs,
        Finished:     self.Finished,
        ExitCode:     self.ExitCode,
    }
    status.Outputs = make(map[string]string)
    for outputIdx, output := range self.Outputs {
        func() {
            self.OutputLocks[outputIdx].Lock()
            defer self.OutputLocks[outputIdx].Unlock()
            status.Outputs[self.Script.OutputDefs[outputIdx].Name] = output.String()
        }()
    }
    return status
}

// Return a string that represents this `ScriptRunStatus`
func (self *ScriptRunStatus) String() string {
    var statBuf bytes.Buffer
    statBuf.WriteString(fmt.Sprintf("%s name %s\n", self.Id, self.ScriptName))
    statBuf.WriteString(fmt.Sprintf("%s id %s\n", self.Id, self.Id))
    for key, val := range self.Params {
        statBuf.WriteString(fmt.Sprintf("%s param %s %s\n", self.Id, key, val))
    }
    for key, val := range self.Outputs {
        statBuf.WriteString(fmt.Sprintf("%s output %s %s\n", self.Id, key, val))
    }
    statBuf.WriteString(fmt.Sprintf("%s timeout_set_ts %d\n", self.Id, self.TimeoutSetTs))
    statBuf.WriteString(fmt.Sprintf("%s start_ts %d\n", self.Id, self.StartTs))
    statBuf.WriteString(fmt.Sprintf("%s finish_ts %d\n", self.Id, self.FinishTs))
    statBuf.WriteString(fmt.Sprintf("%s finished %t\n", self.Id, self.Finished))
    statBuf.WriteString(fmt.Sprintf("%s exit_code %d\n", self.Id, self.ExitCode))
    return statBuf.String()
}
