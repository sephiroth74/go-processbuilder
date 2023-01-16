package processbuilder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

var (
	ErrNoCommands         = errors.New("at least one command is required")
	ErrProcAlreadyStarted = errors.New("process already started")
	ErrProcNotStarted     = errors.New("process not started")
)

type command struct {
	command string
	args    []string

	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter
	cmd        *exec.Cmd
	exitCode   int
}

func (c command) close() {
	if c.pipeWriter != nil {
		c.pipeWriter.Close()
	}
	if c.pipeReader != nil {
		c.pipeReader.Close()
	}
}

func (c command) String() string {
	return fmt.Sprintf("%s %s", c.command, strings.Join(c.args, " "))
}

type Option struct {
	timeout    time.Duration
	stdout     io.Writer
	stdoutPipe bool
}

type Processbuilder struct {
	cmds       []*command
	started    bool
	exited     bool
	cancelFn   context.CancelFunc
	Ctx        context.Context
	StdoutPipe io.ReadCloser
}

func (p *Processbuilder) close() {
	p.exited = true
	for _, command := range p.cmds {
		command.close()
	}
}

func (p *Processbuilder) prepare(option Option) (*Processbuilder, error) {
	if len(p.cmds) == 0 {
		return nil, ErrNoCommands
	}

	total := len(p.cmds)
	fmt.Printf("total commands: %d\n", total)

	var cancel context.CancelFunc
	var ctx context.Context

	if option.timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), option.timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	p.Ctx = ctx
	p.cancelFn = cancel

	// prepare commands
	for index, command := range p.cmds {
		fmt.Printf("%d/%d preparing %s\n", index, total, command.String())

		command.cmd = exec.CommandContext(ctx, command.command, command.args...)

		if index > 0 {
			command.cmd.Stdin = p.cmds[index-1].pipeReader
		}

		if index < total-1 {
			pipeReader, pipeWriter := io.Pipe()
			command.pipeWriter = pipeWriter
			command.pipeReader = pipeReader
			command.cmd.Stdout = pipeWriter
		}

		if index == total-1 {
			if option.stdoutPipe {
				pipe, err := command.cmd.StdoutPipe()
				if err != nil {
					return nil, err
				}
				p.StdoutPipe = pipe
			} else if option.stdout != nil {
				command.cmd.Stdout = option.stdout
			}
		}
	}

	return p, nil
}

func (p *Processbuilder) output(option Option) ([]byte, int, error) {
	var outBuffer bytes.Buffer

	option.stdout = &outBuffer
	result, err := p.prepare(option)

	if err != nil {
		return nil, 0, err
	}

	total := len(p.cmds)

	defer result.cancelFn()
	defer p.close()

	if err := Start(p); err != nil {
		return nil, 0, err
	}

	if _, _, err := Wait(p); err != nil {
		return nil, 0, err
	}

	return outBuffer.Bytes(), p.cmds[total-1].exitCode, nil
}

func Pipe(cmd ...*command) (*Processbuilder, error) {
	return PipeWithTimeout(0, cmd...)
}

func PipeWithTimeout(timeout time.Duration, cmd ...*command) (*Processbuilder, error) {
	p := &Processbuilder{cmds: cmd}

	o := Option{
		stdoutPipe: true,
		timeout:    timeout,
	}

	result, err := p.prepare(o)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func Output(cmds ...*command) ([]byte, int, error) {
	return OutputWithTimeout(0, cmds...)
}

func OutputWithTimeout(timeout time.Duration, cmds ...*command) ([]byte, int, error) {
	p := &Processbuilder{cmds: cmds}
	return p.output(Option{timeout: timeout})
}

func Start(p *Processbuilder) error {
	if p.started || p.exited {
		return ErrProcAlreadyStarted
	}

	p.started = true
	total := len(p.cmds)

	for index, command := range p.cmds {
		fmt.Printf("%d/%d calling start on command %s\n", index, total, command.String())
		if err := command.cmd.Start(); err != nil {
			return err
		}
	}
	return nil
}

func Wait(p *Processbuilder) (int, *exec.Cmd, error) {
	total := len(p.cmds)

	if !p.started || p.exited {
		return 0, nil, ErrProcNotStarted
	}

	defer p.close()

	for index, command := range p.cmds {
		fmt.Printf("%d/%d calling wait on command %s\n", index, total, command.String())

		err := command.cmd.Wait()
		exitCode := command.cmd.ProcessState.ExitCode()

		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
		}

		command.exitCode = exitCode

		if command.pipeWriter != nil {
			command.pipeWriter.Close()
		}

		if index > 0 {
			if p.cmds[index-1].pipeReader != nil {
				p.cmds[index-1].pipeReader.Close()
			}
		}

		if err != nil {
			return exitCode, nil, err
		}
	}
	return p.cmds[total-1].exitCode, p.cmds[total-1].cmd, nil
}

func Kill(p *Processbuilder) error {
	if !p.started || p.exited {
		return ErrProcNotStarted
	}
	if len(p.cmds) > 0 {
		err := p.cmds[0].cmd.Process.Kill()
		p.exited = true
		return err
	}

	return nil
}

func Cancel(p *Processbuilder) error {
	if !p.started || p.exited {
		return ErrProcNotStarted
	}
	p.exited = true
	p.cancelFn()
	return nil
}

// Command creates a new os command
func Command(cmd string, args ...string) *command {
	return &command{
		command: cmd,
		args:    args,
	}
}
