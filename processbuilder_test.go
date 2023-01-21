package processbuilder

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// test simple output pipe
func TestSimpleOutput(t *testing.T) {
	outBuf, _, code, _, err := Output(
		EmptyOption(),
		NewCommand("ls", "-la"),
		NewCommand("grep", "process"),
		NewCommand("sed", "s/process/***/g"),
	)

	assert.NilError(t, err)
	assert.Equal(t, 0, code)

	fmt.Println("\nRESULT: ")
	fmt.Println(outBuf)
}

func TestSimpleLogCat(t *testing.T) {
	outBuffer, errBuffer, code, status, err := Output(
		Option{Timeout: 5 * time.Second},
		NewCommand("adb", "logcat", "-v", "brief", "-T", "01-21 13:52:12.664", "WM-DiagnosticsWrkr:V", "*:S"),
	)

	if err != nil {
		fmt.Printf("err: %#v\n", err)
	}

	fmt.Printf("code: %d\n", code)
	fmt.Printf("status: %#v\n", status)

	if outBuffer != nil {
		fmt.Printf("outBuffer: %s\n", outBuffer.String())
	}

	if errBuffer != nil {
		fmt.Printf("errBuffer: %s\n", errBuffer.String())
	}
}

// assume adb is already connected
// test screen mirroring
func TestScreenMirroring(t *testing.T) {
	closeSignal := make(chan os.Signal)
	signal.Notify(closeSignal, os.Interrupt, syscall.SIGTERM)
	defer close(closeSignal)

	p, err := Create(
		Option{Timeout: 30 * time.Second},
		NewCommand("adb", "shell", "while true; do screenrecord --output-format=h264 --size=1024x768 -; done"),
		NewCommand("ffplay", "-framerate", "60", "-probesize", "64", "-sync", "video", "-").
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

	p, err := PipeOutput(
		Option{Timeout: 10 * time.Second},
		NewCommand("adb", "logcat", "-v", "pid", "-T", "01-20 08:52:41.820", "tvlib.RestClient:V *:S"),
		// NewCommand("grep", "RestClient"),
	)
	assert.NilError(t, err)

	pipeOut := p.StdoutPipe

	err = Start(p)
	assert.NilError(t, err)

	found := false

	scanner := bufio.NewScanner(pipeOut)
	for scanner.Scan() {
		text := scanner.Text()
		fmt.Printf("line => %s\n", text)
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

	p, err := PipeOutput(
		Option{},
		NewCommand("adb", "logcat"),
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

func TestScreenRecord(t *testing.T) {
	closeSignal := make(chan os.Signal)
	signal.Notify(closeSignal, os.Interrupt, syscall.SIGTERM)
	defer close(closeSignal)

	outBuf, outErr, code, _, err := Output(
		Option{
			Timeout: 10 * time.Second,
		},
		NewCommand("adb", "shell", "screenrecord --bit-rate 20000000 --time-limit 5 /sdcard/Download/screenrecord.mp4"),
	)

	fmt.Printf("[test] exit code: %d\n", code)

	if code != int(syscall.SIGINT) {
		if err != nil {
			fmt.Println(err.Error())
			fmt.Printf("[test] err: %#v\n", err)
		}

		if outErr != nil {
			fmt.Printf("[test] output: %s\n", outBuf.String())
		}

		if outErr != nil {
			fmt.Printf("[test] out error: %s\n", outErr.String())
		}
	} else {
		fmt.Println("done.")
	}

	// shell screenrecord --bit-rate 20000000 --time-limit 180 /sdcard/Download/screenrecord.mp4
}
