package processbuilder

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func init() {
	Logger.SetOutput(ioutil.Discard)
}

// test simple output pipe
func TestSimpleOutput(t *testing.T) {
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

// assume adb is already connected
// test screen mirroring
func TestScreenMirroring(t *testing.T) {
	closeSignal := make(chan os.Signal)
	signal.Notify(closeSignal, os.Interrupt, syscall.SIGTERM)
	defer close(closeSignal)

	p, err := Create(
		Option{Timeout: 30 * time.Second, Close: &closeSignal},
		Command("adb", "shell", "while true; do screenrecord --output-format=h264 --size=1024x768 -; done"),
		Command("ffplay", "-framerate", "60", "-probesize", "64", "-sync", "video", "-").
			WithStdErr(os.Stderr).
			WithStdOut(os.Stdout),
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

func TestLogcatPipe(t *testing.T) {
	log.Default().Println("TestLogcatPipe")
	closeSignal := make(chan os.Signal)
	signal.Notify(closeSignal, os.Interrupt, syscall.SIGTERM)
	defer close(closeSignal)

	p, err := Pipe(
		Option{Close: &closeSignal},
		Command("adb", "logcat"),
		Command("grep", "WARNING"),
	)
	assert.NilError(t, err)

	pipeOut := p.StdoutPipe

	err = Start(p)
	assert.NilError(t, err)

	found := false

	scanner := bufio.NewScanner(pipeOut)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.Contains(text, "WARNING") {
			fmt.Printf("line => %s\n", text)
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

func TestSimpleLogcat(t *testing.T) {
	closeSignal := make(chan os.Signal)
	signal.Notify(closeSignal, os.Interrupt, syscall.SIGTERM)
	defer close(closeSignal)

	p, err := Pipe(
		Option{Close: &closeSignal},
		Command("adb", "logcat"),
	)
	assert.NilError(t, err)

	pipe := p.StdoutPipe

	err = Start(p)
	assert.NilError(t, err)

	found := false

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		text := scanner.Text()
		fmt.Printf("line => %s\n", text)

		if strings.Contains(text, "swisscom") {
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
