package processbuilder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	streams "github.com/sephiroth74/go_streams"
)

var (
	consoleWriter         zerolog.ConsoleWriter = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC822Z}
	Logger                *zerolog.Logger       = nil
	ErrNoCommands         error                 = errors.New("at least one command is required")
	ErrProcAlreadyStarted error                 = errors.New("process already started")
	ErrProcNotStarted     error                 = errors.New("process not started")
)

type ExitStatus string

const (
	ExitStatusNil      ExitStatus = "<nil>"
	ExitStatusExited   ExitStatus = "exited"
	ExitStatusSignaled ExitStatus = "signaled"
	ExitStatusStopped  ExitStatus = "stopped"
)

func init() {
	defaultLogger := zerolog.New(consoleWriter).Level(zerolog.TraceLevel)
	Logger = &defaultLogger
}

func getExitCode(cmd *exec.Cmd, err error) int {
	code := cmd.ProcessState.ExitCode()
	if e2, ok := err.(*exec.ExitError); ok {
		if s, ok := e2.Sys().(syscall.WaitStatus); ok {
			if s.Signaled() {
				code = int(syscall.SIGINT)
			} else {
				code = int(s.ExitStatus())
			}
		}
	}
	return code
}

func SetLogger(logger *zerolog.Logger) {
	Logger = logger
}

type Command struct {
	command string
	args    []string

	StdOut io.Writer
	StdErr io.Writer
	StdIn  io.Reader

	cmd        *exec.Cmd
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter
	exitCode   int
}

type Option struct {
	Timeout    time.Duration
	LogLevel   zerolog.Level
	stdoutPipe bool
}

func EmptyOption() Option {
	return Option{}
}

type Processbuilder struct {
	cmds   []*Command
	option *Option

	started    bool
	exited     bool
	killed     bool
	cancelFn   context.CancelFunc
	Ctx        context.Context
	StdoutPipe io.ReadCloser
	StdErrPipe io.ReadCloser
}

func (p *Processbuilder) String() string {
	cmds := streams.Map(p.cmds, func(data *Command) string {
		return data.String()
	})
	return fmt.Sprintf("ProcessBuilder(%s, started=%t, exited=%t)", strings.Join(cmds, " | "), p.started, p.exited)
}

func (p *Processbuilder) Count() int {
	return len(p.cmds)
}

func (p *Processbuilder) GetCmd(index int) *exec.Cmd {
	return p.cmds[index].cmd
}

func (p *Processbuilder) close() {
	p.exited = true
	for _, command := range p.cmds {
		command.close()
	}
}

func (p *Processbuilder) prepare() (*Processbuilder, error) {
	total := len(p.cmds)

	if total == 0 {
		return nil, ErrNoCommands
	}

	cmds := streams.Map(p.cmds, func(data *Command) string {
		return data.String()
	})

	if Logger != nil && p.option.LogLevel <= zerolog.DebugLevel {
		Logger.Debug().Msgf("Executing `%s`", strings.Join(cmds, " | "))
	}

	var cancel context.CancelFunc
	var ctx context.Context

	if p.option.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), p.option.Timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	p.Ctx = ctx
	p.cancelFn = cancel
	var previousCommand *Command

	// prepare commands
	for index, command := range p.cmds {
		if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
			Logger.Trace().Msgf("%d/%d preparing %s", index, total, command.String())
		}

		command.cmd = exec.CommandContext(ctx, command.command, command.args...)
		// command.cmd = exec.Command(command.command, command.args...)

		// checks

		if index != 0 && command.StdIn != nil {
			return nil, errors.New("stdin allowed only for the first command")
		}

		if index != total-1 && command.StdOut != nil {
			return nil, errors.New("stdout allowed only for the last command")
		}

		if command.StdErr != nil {
			command.cmd.Stderr = command.StdErr
		}

		// first command
		if index == 0 {
			if command.StdIn != nil {
				command.cmd.Stdin = command.StdIn
			}
		}

		// second .. last
		if index > 0 {
			previousCommand = p.cmds[index-1]
			command.cmd.Stdin = previousCommand.pipeReader
		}

		// first .. second to last
		if index < total-1 {
			pipeReader, pipeWriter := io.Pipe()
			command.pipeWriter = pipeWriter
			command.pipeReader = pipeReader
			command.cmd.Stdout = pipeWriter
		}

		// last command
		if index == total-1 {
			if p.option.stdoutPipe {
				if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
					Logger.Trace().Msgf("using cmd.StdoutPipe on '%s'", command.String())
				}
				pipe, err := command.cmd.StdoutPipe()
				if err != nil {
					return nil, err
				}
				p.StdoutPipe = pipe

				pipeErr, err := command.cmd.StderrPipe()
				if err != nil {
					return nil, err
				}
				p.StdErrPipe = pipeErr

			} else {
				if command.StdOut != nil {
					if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
						Logger.Trace().Msgf("using cmd.StdOut on '%s'", command.String())
					}
					command.cmd.Stdout = command.StdOut
				}
			}
		}
	}

	return p, nil
}

func (p *Processbuilder) output() (*bytes.Buffer, *bytes.Buffer, int, *os.ProcessState, error) {
	var total = p.Count()
	var outBuffer bytes.Buffer
	var errBuffer bytes.Buffer

	if total == 0 {
		return nil, nil, -1, nil, ErrNoCommands
	}

	lastCommand := p.cmds[total-1]

	if lastCommand.StdOut == nil {
		lastCommand.StdOut = &outBuffer
	}

	lastCommand.StdErr = &errBuffer

	_, err := p.prepare()

	if err != nil {
		return nil, &errBuffer, 0, nil, err
	}

	if err := Start(p); err != nil {
		return &outBuffer, &errBuffer, -1, nil, err
	}

	code, state, err := Wait(p)

	if err != nil {
		return &outBuffer, &errBuffer, code, state, err
	}

	return &outBuffer, &errBuffer, code, state, nil
}

func PipeOutput(option Option, cmd ...*Command) (*Processbuilder, error) {
	option.stdoutPipe = true
	p := &Processbuilder{cmds: cmd, option: &option}
	return p.prepare()
}

func Create(option Option, cmd ...*Command) (*Processbuilder, error) {
	p := &Processbuilder{cmds: cmd, option: &option}
	return p.prepare()
}

func Output(option Option, cmds ...*Command) (*bytes.Buffer, *bytes.Buffer, int, *os.ProcessState, error) {
	p := &Processbuilder{cmds: cmds, option: &option}
	return p.output()
}

func Start(p *Processbuilder) error {
	if p.started || p.exited {
		defer p.cancelFn()
		defer p.close()
		return ErrProcAlreadyStarted
	}

	p.started = true
	total := len(p.cmds)

	for index, command := range p.cmds {
		if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
			Logger.Trace().Msgf("%d/%d calling start on command %s", index, total, command.String())
		}

		if err := command.cmd.Start(); err != nil {
			defer p.cancelFn()
			defer p.close()
			return err
		}
	}
	return nil
}

func Run(p *Processbuilder) (int, *os.ProcessState, error) {
	total := len(p.cmds)

	defer p.cancelFn()
	defer p.close()

	if p.started || p.exited {
		return -1, nil, ErrProcAlreadyStarted
	}

	p.started = true

	closeChannel := make(chan os.Signal, 1)
	signal.Notify(closeChannel, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(closeChannel)
	defer close(closeChannel)

	var previousCommand *Command
	var lastCommand = p.cmds[total-1]

	for index, command := range p.cmds {
		if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
			Logger.Trace().Msgf("%d/%d calling run on command %s", index, total, command.String())
		}

		if err := command.cmd.Run(); err != nil {
			if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
				Logger.Trace().Msgf("%d/%d run exited with error %s", index, total, err.Error())
			}

			exitCode := getExitCode(command.cmd, err)
			if p.killed {
				exitCode = int(syscall.SIGINT)
			}

			return exitCode, command.cmd.ProcessState, err
		}

		exitCode := command.cmd.ProcessState.ExitCode()
		command.exitCode = exitCode

		if command.pipeWriter != nil {
			command.pipeWriter.Close()
		}

		if index > 0 {
			previousCommand = p.cmds[index-1]
			if previousCommand.pipeReader != nil {
				previousCommand.pipeReader.Close()
			}
		}
	}

	return lastCommand.exitCode, lastCommand.cmd.ProcessState, nil
}

func Wait(p *Processbuilder) (int, *os.ProcessState, error) {
	total := len(p.cmds)

	defer p.cancelFn()
	defer p.close()

	if !p.started || p.exited {
		return -1, nil, ErrProcNotStarted
	}

	closeChannel := make(chan os.Signal, 1)
	signal.Notify(closeChannel, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(closeChannel)
	defer close(closeChannel)

	var previousCommand *Command
	var lastCommand = p.cmds[total-1]

	for index, command := range p.cmds {
		if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
			Logger.Trace().Msgf("%d/%d calling wait on command %s", index, total, command.String())
		}

		if err := command.cmd.Wait(); err != nil {
			if Logger != nil && p.option.LogLevel <= zerolog.TraceLevel {
				Logger.Trace().Msgf("%d/%d wait exited with error %s", index, total, err.Error())
			}
			exitCode := getExitCode(command.cmd, err)
			if p.killed {
				exitCode = int(syscall.SIGINT)
			}
			return exitCode, command.cmd.ProcessState, err
		}

		exitCode := command.cmd.ProcessState.ExitCode()
		command.exitCode = exitCode

		if command.pipeWriter != nil {
			command.pipeWriter.Close()
		}

		if index > 0 {
			previousCommand = p.cmds[index-1]
			if previousCommand.pipeReader != nil {
				previousCommand.pipeReader.Close()
			}
		}
	}

	return lastCommand.exitCode, lastCommand.cmd.ProcessState, nil
}

func Kill(p *Processbuilder) error {
	if Logger != nil && p.option.LogLevel <= zerolog.DebugLevel {
		Logger.Debug().Msg("Killing process...")
	}

	p.killed = true

	if !p.started || p.exited {
		return ErrProcNotStarted
	}

	if p.Count() > 0 {
		err := p.cmds[0].cmd.Process.Kill()
		p.killed = true
		p.exited = true
		return err
	}

	return nil
}

func Cancel(p *Processbuilder) error {
	if Logger != nil && p.option.LogLevel <= zerolog.DebugLevel {
		Logger.Debug().Msgf("Cancelling process...")
	}

	p.killed = true

	if !p.started || p.exited {
		println("already cancelled!")
		return ErrProcNotStarted
	}

	p.exited = true
	p.cancelFn()
	return nil
}

// NewCommand creates a new os command
func NewCommand(cmd string, args ...string) *Command {
	return &Command{
		command: cmd,
		args:    args,
	}
}

func (c *Command) WithStdErr(w io.Writer) *Command {
	c.StdErr = w
	return c
}

func (c *Command) WithStdOut(w io.Writer) *Command {
	c.StdOut = w
	return c
}

func (c *Command) WithStdIn(r io.Reader) *Command {
	c.StdIn = r
	return c
}

func (c *Command) close() {
	if c.pipeWriter != nil {
		c.pipeWriter.Close()
	}
	if c.pipeReader != nil {
		c.pipeReader.Close()
	}
}

func (c *Command) String() string {
	return fmt.Sprintf("%s %s", filepath.Base(c.command), strings.Join(c.args, " "))
}
