package processbuilder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

	StdOut io.Writer
	StdErr io.Writer
	StdIn  io.Reader

	cmd        *exec.Cmd
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter
	exitCode   int
}

type Option struct {
	Timeout time.Duration
	Close   *chan os.Signal

	stdoutPipe bool
}

func EmptyOption() Option {
	return Option{}
}

type Processbuilder struct {
	cmds   []*command
	option *Option

	started    bool
	exited     bool
	cancelFn   context.CancelFunc
	Ctx        context.Context
	StdoutPipe io.ReadCloser
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

	var cancel context.CancelFunc
	var ctx context.Context

	if p.option.Timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), p.option.Timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	p.Ctx = ctx
	p.cancelFn = cancel

	var previousCommand *command

	// prepare commands
	for index, command := range p.cmds {
		fmt.Printf("%d/%d preparing %s\n", index, total, command.String())

		command.cmd = exec.CommandContext(ctx, command.command, command.args...)

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
			fmt.Printf("pipereader=%v, pipewriter=%v\n", pipeReader, pipeWriter)

			command.pipeWriter = pipeWriter
			command.pipeReader = pipeReader
			command.cmd.Stdout = pipeWriter
		}

		// last command
		if index == total-1 {
			if command.StdOut != nil {
				command.cmd.Stdout = command.StdOut
			}
			// if p.option.stdoutPipe {
			// 	pipe, err := command.cmd.StdoutPipe()
			// 	if err != nil {
			// 		return nil, err
			// 	}
			// 	p.StdoutPipe = pipe
			// } else if p.option.stdout != nil {
			// 	fmt.Printf("[%d] %s stdout = option.stdout\n", index, command.command)
			// 	command.cmd.Stdout = p.option.stdout
			// } else {
			// 	fmt.Printf("[%d] %s stdout = os.Stdout\n", index, command.command)
			// 	command.cmd.Stdout = os.Stdout
			// }
		}
	}

	return p, nil
}

func (p *Processbuilder) output() (*bytes.Buffer, int, error) {
	var total = p.Count()
	var outBuffer bytes.Buffer

	if total > 0 {
		if p.cmds[total-1].StdOut != nil {
			return nil, -1, errors.New("stdout not allowed on the last command")
		}
		p.cmds[total-1].StdOut = &outBuffer
	}

	_, err := p.prepare()

	if err != nil {
		return nil, 0, err
	}

	defer p.cancelFn()
	defer p.close()

	if err := Start(p); err != nil {
		return nil, 0, err
	}

	if _, _, err := Wait(p); err != nil {
		return nil, -1, err
	}

	return &outBuffer, p.cmds[total-1].exitCode, nil
}

func Pipe(option Option, cmd ...*command) (*Processbuilder, error) {
	option.stdoutPipe = true
	p := &Processbuilder{cmds: cmd, option: &option}
	return p.prepare()
}

func Create(option Option, cmd ...*command) (*Processbuilder, error) {
	p := &Processbuilder{cmds: cmd, option: &option}
	return p.prepare()
}

func Output(option Option, cmds ...*command) (*bytes.Buffer, int, error) {
	p := &Processbuilder{cmds: cmds, option: &option}
	return p.output()
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

	if p.option.Close != nil {
		go func() {
			<-*p.option.Close
			fmt.Println("Received kill signal!")
			Kill(p)
		}()
	}

	var previousCommand *command
	var lastCommand = p.cmds[total-1]

	for index, command := range p.cmds {
		fmt.Printf("%d/%d calling wait on command %s\n", index, total, command.String())

		if err := command.cmd.Wait(); err != nil {
			return command.cmd.ProcessState.ExitCode(), nil, err
		}

		exitCode := command.cmd.ProcessState.ExitCode()
		command.exitCode = exitCode

		if command.pipeWriter != nil {
			fmt.Printf("[%d] closing pipeWripter %v of command %s\n", index, command.pipeReader, command.command)
			command.pipeWriter.Close()
		}

		if index > 0 {
			previousCommand = p.cmds[index-1]
			if previousCommand.pipeReader != nil {
				fmt.Printf("[%d] closing pipeReader %v of command %s\n", index, previousCommand.pipeReader, command.command)
				previousCommand.pipeReader.Close()
			}
		}
	}

	fmt.Printf("exitCode=%d\n", lastCommand.exitCode)
	return lastCommand.exitCode, lastCommand.cmd, nil
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

func (c *command) WithStdErr(w io.Writer) *command {
	c.StdErr = w
	return c
}

func (c *command) WithStdOut(w io.Writer) *command {
	c.StdOut = w
	return c
}

func (c *command) WithStdIn(r io.Reader) *command {
	c.StdIn = r
	return c
}

func (c *command) close() {
	if c.pipeWriter != nil {
		c.pipeWriter.Close()
	}
	if c.pipeReader != nil {
		c.pipeReader.Close()
	}
}

func (c *command) String() string {
	return fmt.Sprintf("%s %s", c.command, strings.Join(c.args, " "))
}
