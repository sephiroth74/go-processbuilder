package processbuilder

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestOutput(t *testing.T) {
	outBuf, code, err := Output(
		EmptyOption(),
		Command("ls", "-la"),
		Command("grep", "process"),
		Command("sed", "s/process/***/g"),
	)

	assert.NilError(t, err)
	assert.Equal(t, 0, code)

	fmt.Println("\nRESULT: ")
	fmt.Println(outBuf)
}

func TestScreenRecord(t *testing.T) {
	r, w := io.Pipe()
	var errBuf bytes.Buffer
	// var outBuf bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd1 := exec.CommandContext(ctx, "adb", "shell", "while true; do screenrecord --output-format=h264 -; done")
	cmd1.Stdout = w
	cmd1.Stderr = &errBuf

	cmd2 := exec.CommandContext(ctx, "ffplay", "-framerate", "60", "-probesize", "32", "-sync", "video", "-")
	cmd2.Stdin = r
	cmd2.Stdout = os.Stdout

	if err := cmd1.Start(); err != nil {
		fmt.Println("Error during strt of cmd1")
		fmt.Println(err)
		return
	}

	if err := cmd2.Start(); err != nil {
		fmt.Println("Error during start of cmd2")
		fmt.Println(err)
	}

	if err := cmd1.Wait(); err != nil {
		fmt.Println("Error during wait of cmd1")
		fmt.Println(err)

		fmt.Println(errBuf.String())
	}

	if err := cmd2.Wait(); err != nil {
		w.Close()
		r.Close()
		fmt.Println("Error during wait of cmd2")
		fmt.Println(err)
	}

}

func TestAgain(t *testing.T) {
	closeSignal := make(chan os.Signal)
	signal.Notify(closeSignal, os.Interrupt, syscall.SIGTERM)

	p, err := Create(
		Option{Timeout: 30 * time.Second, Close: &closeSignal},
		Command("adb", "shell", "while true; do screenrecord --output-format=h264 --size=1024x768 -; done"),
		Command("ffplay", "-framerate", "60", "-probesize", "64", "-sync", "video", "-").WithStdErr(os.Stderr).WithStdOut(os.Stdout),
	)
	assert.NilError(t, err)

	err = Start(p)
	assert.NilError(t, err)

	if err != nil {
		fmt.Println(err)
	}

	exitCode, _, err := Wait(p)

	if err != nil {
		fmt.Println(err)
	}

	assert.NilError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestCancel(t *testing.T) {

	p, err := Create(
		Option{Timeout: 30 * time.Second},
		Command("adb", "shell", "while true; do screenrecord --output-format=h264 -; done"),
		Command("ffplay", "-framerate", "60", "-probesize", "32", "-sync", "video", "-"),
	)
	assert.NilError(t, err)

	err = Start(p)
	assert.NilError(t, err)

	if err != nil {
		fmt.Println(err)
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		fmt.Println("Kill Process!")
		Kill(p)
	}()

	exitCode, _, err := Wait(p)

	if err != nil {
		fmt.Println(err)
	}

	assert.NilError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestOutputPipe(t *testing.T) {
	p, err := Pipe(
		Option{},
		Command("adb", "logcat"),
		Command("grep", "Unix"),
	)
	assert.NilError(t, err)

	pipe := p.StdoutPipe

	err = Start(p)
	assert.NilError(t, err)

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		fmt.Println("Kill Process!")
		Kill(p)
	}()

	found := false

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		text := scanner.Text()

		f := regexp.MustCompile(`failed to create Unix domain socket: Operation not permitted`)
		match := f.FindAllString(text, -1)
		if len(match) > 0 {
			fmt.Println(text)
			fmt.Println("******** OK DONE!!!! **************")
			found = true
			Cancel(p)
			break
		}
	}

	exit, _, err := Wait(p)

	assert.Equal(t, true, found)

	if !found {
		fmt.Printf("err: %#v\n", err)
		fmt.Printf("exit: %d\n", exit)
		assert.NilError(t, err)
		assert.Equal(t, 0, exit)
	}

}
