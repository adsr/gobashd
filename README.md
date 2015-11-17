gobashd
=======

gobashd executes long-running bash scripts asynchronously on behalf of
network-connected clients. It supports typed script params, multiple output
variables, script timeouts, async status lookups, and async kills. Scripts are
written in [text/template](http://golang.org/pkg/text/template/)-templated bash
with some metadata in leading comments. Two server protocols are currently
supported: (1) JSON over HTTP, and (2)
[net/textproto](http://golang.org/pkg/net/textproto/).

gobashd is not intended for executing remote commands synchronously. For that,
use SSH or telnet.

At Etsy, our primary use case for gobashd is mysql backups and restores. These
long-running processes involve various binaries (mysql, mysqld, netcat,
innobackupex, xbstream, snzip, tar, etc.) making them suitable for shell
scripting. However we also desire queryable instrumentation beyond stdout,
stderr, and exit codes that is (while not impossible) difficult to achieve in
a single shell script. At the time of writing, gobashd has been running in
production at Etsy for 8 months.

**Example**

Consider the following bash script, `/foo/bar/lottery.sh`, and let's pretend we
want to let clients run this remotely and asynchronously for some reason.

    #/bin/bash
    #
    # @desc A lottery example
    #
    # @param name string `Adam` Name of player
    # @param odds float `0.01` Odds of winning lottery
    # @param num_tickets int `100` Number of tickets
    # @param verbose bool `false` Whether to be verbose
    # @param win_cmd unsafe `echo You won!` A raw unescaped (unsafe!) string
    #
    # @output current_ticket w

    {{ if .verbose }}
    echo Hello {{ .name }}, let's play the lottery
    {{ end }}

    winner='n'
    for ticket in $(seq 1 {{ .num_tickets }}); do
        echo $ticket >&$current_ticket
        if (printf 'scale=2; %f/32767 <= %f \n' $RANDOM {{ .odds }} | bc); then
            winner='y'
            break
        fi
        sleep 0.1
    done

    if [ $winner == 'y' ]; then
        {{ .win_cmd }}
    fi

Note the script looks like a regular bash scripts except for two things: (1)
some metadata in the leading comments, and (2) text/template-style templating.
Now let's start gobashd, instructing it to look for scripts in `/foo/bar`, and
to listen for net/textproto clients on port 1234:

    $ gobashd -d /foo/bar/ -t ':1234'
    [I] 2014/11/13 20:54:20 Loaded script lottery.sh

Now `lottery.sh` is available for clients to use. Scripts are only loaded if
they are readable and executable by the user running gobashd.

Invoke `lottery.sh` with verbose=true via the net/textproto protocol like so.

    $ echo lottery.sh verbose=true | nc localhost 1234
    OK 200
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a name lottery.sh
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a id 348ef817-82ef-71bf-5cfb-9ceb0db92c4a
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param name 'Adam'
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param odds 0.01
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param num_tickets 100
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param verbose true
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param win_cmd echo You won!
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a output current_ticket
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a timeout_set_ts 1415913651
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a start_ts 0
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a finish_ts 0
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a finished false
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a exit_code 0

Take note of a few things: (1) The server fills in defaults for parameters the
client didn't specify; (2) The server returns immediately even though the
underlying `lottery.sh` is still running; (3) We now have an id
`348ef817-82ef-71bf-5cfb-9ceb0db92c4a` which uniquely identifies this
invocation of the script, even after it's done running.

While it's running, query its status.

    $ echo status | nc localhost 1234
    OK 200
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a name lottery.sh
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a id 348ef817-82ef-71bf-5cfb-9ceb0db92c4a
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param name 'Adam'
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param odds 0.01
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param num_tickets 100
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param verbose true
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param win_cmd echo You won!
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a output current_ticket 13
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a timeout_set_ts 1415913651
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a start_ts 1415913651
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a finish_ts 0
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a finished false
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a exit_code 0

Note `current_ticket 13` and `finished false`. Wait a bit and query again:

    $ echo status | nc localhost 1234
    OK 200
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a name lottery.sh
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a id 348ef817-82ef-71bf-5cfb-9ceb0db92c4a
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param name 'Adam'
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param odds 0.01
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param num_tickets 100
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param verbose true
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a param win_cmd echo You won!
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a output current_ticket 49
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a timeout_set_ts 1415913651
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a start_ts 1415913651
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a finish_ts 1415913656
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a finished true
    348ef817-82ef-71bf-5cfb-9ceb0db92c4a exit_code 0

Now it's finished.

Switching to the server console, we see the following output. This is a mix of
gobashd internal logging and stdout/stderr from running scripts.

    [I] 2014/11/13 21:20:51 [lottery.sh] [348ef817-82ef-71bf-5cfb-9ceb0db92c4a] echo Hello Adam, let's play the lottery
    [I] 2014/11/13 21:20:51 [lottery.sh] [348ef817-82ef-71bf-5cfb-9ceb0db92c4a] current_ticket: 1
    [I] 2014/11/13 21:20:51 [lottery.sh] [348ef817-82ef-71bf-5cfb-9ceb0db92c4a] current_ticket: 2
    ...
    [I] 2014/11/13 21:20:56 [lottery.sh] [348ef817-82ef-71bf-5cfb-9ceb0db92c4a] current_ticket: 49
    [I] 2014/11/13 21:20:56 [lottery.sh] [348ef817-82ef-71bf-5cfb-9ceb0db92c4a] You won!
    [I] 2014/11/13 21:20:56 [lottery.sh] [348ef817-82ef-71bf-5cfb-9ceb0db92c4a] Finished ExitCode=0

And that's about it for a simple example.

Still need to document the following:

* Special fds `$_timeout` and `$_clear`
* Append outputs
* Commands `kill` and `purge`
* JSON interface
* SIGHUP'ing the server to reload scripts

**TODO**

* SASL
* Basic WHERE clauses for status command
* Ability use other shells
* Ability to pass arguments to shell

**Security**

The daemon will only parse and execute scripts that are readable and executable
by the user currently running gobashd. E.g., if gobashd is running as mysql, it
will only run scripts that are owned by mysql and have `r-x------` perm bits.
Additionally, only regular files are parsed, not symlinks, special files, etc.

Currently there is no concept of authentication in gobashd. Any client able to
connect to gobashd can invoke scripts. Please keep this in mind when deploying
gobashd.
