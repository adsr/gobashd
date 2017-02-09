package main

import (
    "bufio"
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "path"
    "regexp"
    "strings"
    "text/template"
    "time"
)

type Script struct {
    Name               string
    Help               string
    Desc               string
    ParamDefs          []ScriptDef `json:"-"`
    OutputDefs         []ScriptDef `json:"-"`
    Path               string
    *template.Template `json:"-"`
    ParsedTs           int64
}

type ScriptDef struct {
    Name       string
    Type       string
    DefaultStr string
    Default    interface{}
    Desc       string
}

// Make a `Script` out of the bash script at `scriptPath` with contents
// `source`. The script should be a regular bash script formatted as a
// `text/template.Template`. Optionally, a description, params, and outputs
// may be defined in the leading comments of the script. The format for these
// are as follows:
//
//     # @desc <text>
//     # @param <pname> (int|float|string|unsafe|bool) `<default>` <pdesc>
//     # @output <vname> (a|w)
//
// @desc
//     desc entries get appended to `Script.Desc`.
// @param
//     param entries define paramters useable as `text/template` vars within
//     the script. Use `int` for integers, `float` for floats, `string` for
//     shell-escaped strings, `bool` for booleans, and `unsafe` for unescaped
//     strings.
// @output
//     output entries define simple output vars to write within the script.
//     Use `a` for an append var and `w` for an overwrite var. Write to
//     these vars like so:
//         echo 'hi' >&$vname
//     Clear an append var like so:
//         echo 'vname' >&$_clear
//
// Scripts may also set a timeout at any time like so:
//     echo 100 >&$_timeout   # timeout 100 seconds from now
//     echo 0 >&$_timeout     # disable timeout (default)
// If a script times out, it is sent a kill signal.
func newScript(scriptPath string, source []byte) (*Script, error) {
    script := &Script{
        Name:       path.Base(scriptPath),
        Path:       scriptPath,
        ParamDefs:  make([]ScriptDef, 0),
        OutputDefs: make([]ScriptDef, 0),
    }

    // Define regexes
    descRe := regexp.MustCompile(`(?m)^#\s+@desc\s*(.*)$`)
    paramRe := regexp.MustCompile(fmt.Sprintf(
        `(?m)^#\s+@param\s+([^\s]+)\s+(int|float|string|bool|unsafe)\s+%s([^%s]*)%s\s+(.*)$`, "`", "`", "`"))
    outputRe := regexp.MustCompile(`(?m)^#\s+@output\s+([^\s]+)\s+(a|w)$`)

    // Read source line by line
    reader := bufio.NewReader(bytes.NewBuffer(source))
    var templateBuf bytes.Buffer
    var helpBuf bytes.Buffer
    afterLeadingComments := false
    for {
        line, readErr := reader.ReadString('\n')
        if readErr == io.EOF {
            // End of source
            break
        } else if !afterLeadingComments && !strings.HasPrefix(line, "#") {
            // End of leading comments
            // Insert _clear, _timeout, and output var fds
            if _, writeErr := templateBuf.WriteString("_clear=3\n_timeout=4\n"); writeErr != nil {
                return nil, writeErr
            }
            for outputDefIdx, outputDef := range script.OutputDefs {
                if _, writeErr := templateBuf.WriteString(fmt.Sprintf("%s=%d\n", outputDef.Name, 5+outputDefIdx)); writeErr != nil {
                    return nil, writeErr
                }
            }
            afterLeadingComments = true
        } else if readErr != nil {
            // Some other error reading source
            errLog.Printf("reader.ReadString err=%v\n", readErr)
            return nil, readErr
        }
        if _, writeErr := templateBuf.WriteString(line); writeErr != nil {
            return nil, writeErr
        }
        matchedEntry := true
        if afterLeadingComments {
            // After leading comments, so do nothing
            continue
        } else if matches := descRe.FindStringSubmatch(line); len(matches) > 0 {
            // Matched a @desc entry
            script.Desc += strings.TrimSpace(matches[1])
        } else if matches := paramRe.FindStringSubmatch(line); len(matches) > 0 {
            // Matched a @param entry
            paramDef := &ScriptDef{
                Name:       matches[1],
                Type:       matches[2],
                DefaultStr: matches[3],
                Desc:       matches[4],
            }
            if valErr := paramDef.makeDefault(); valErr != nil {
                errLog.Printf("paramDef.makeDefault err=%v\n", valErr)
                return nil, valErr
            }
            script.ParamDefs = append(script.ParamDefs, *paramDef)
        } else if matches := outputRe.FindStringSubmatch(line); len(matches) > 0 {
            // Mached an @output entry
            script.OutputDefs = append(script.OutputDefs, ScriptDef{
                Name: matches[1],
                Type: matches[2],
            })
        } else {
            matchedEntry = false
        }
        if matchedEntry {
            helpBuf.WriteString(line)
        }
    }

    // Make help
    script.Help = helpBuf.String()

    // Compile template
    if tpl, tplErr := template.New(script.Name).Parse(templateBuf.String()); tplErr != nil {
        return nil, tplErr
    } else {
        script.Template = tpl
    }

    // Done!
    script.ParsedTs = time.Now().Unix()
    return script, nil
}

// Given a `map[string]string` of input params `iparams`, return a
// `map[string]interface{}` of type-normalized params. Params missing from
// `iparams` are set to their default values.
func (self *Script) normalizeParams(iparams map[string]string) (map[string]interface{}, error) {
    oparams := make(map[string]interface{})
    for _, def := range self.ParamDefs {
        if ival, exists := iparams[def.Name]; exists {
            if oval, err := def.toInterfaceVal(ival); err != nil {
                return nil, err
            } else {
                oparams[def.Name] = oval
            }
        } else {
            oparams[def.Name] = def.Default
        }
    }
    return oparams, nil
}

// Return the index of the output with name `name`. Return -1 if no such
// output exists.
func (self *Script) getOutputIdxByName(name string) int {
    for i, def := range self.OutputDefs {
        if def.Name == name {
            return i
        }
    }
    return -1
}

// Set `ScriptDef.Default` to the JSON-decoded version of
// `ScriptDef.DefaultStr`
func (self *ScriptDef) makeDefault() error {
    if def, err := self.toInterfaceVal(self.DefaultStr); err != nil {
        return err
    } else {
        self.Default = def
    }
    return nil
}

// Return the JSON-decoded form of `in`. For `unsafe` and `string` types,
// double quotes are added if `in` does not begin with a double quote. For
// `string` the value is passed through `escapeShellArg`. `int` and `float`
// types are both JSON-decoded as floats, but `int` is casted to an integer
// afterwards. `bool` is JSON-decoded as a bool.
func (self *ScriptDef) toInterfaceVal(in string) (interface{}, error) {
    var v interface{}
    if self.Type == "int" || self.Type == "float" {
        v = float64(0)
    } else if self.Type == "string" || self.Type == "unsafe" {
        v = ""
        if !strings.HasPrefix(in, `"`) {
            in = fmt.Sprintf(`"%s"`, in)
        }
    } else if self.Type == "bool" {
        v = false
    } else {
        return nil, errors.New(fmt.Sprintf("Unrecognized type %s for %s", self.Type, self.Name))
    }
    err := json.Unmarshal([]byte(in), &v)
    if err != nil {
        return nil, errors.New(fmt.Sprintf("Unable to parse `%s` as %s (%s)", in, self.Name, self.Type))
    }
    if self.Type == "int" {
        return int(v.(float64)), nil
    } else if self.Type == "string" {
        return escapeShellArg(v.(string)), nil
    }
    return v, nil
}
