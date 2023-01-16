package processbuilder

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"testing"

	"gotest.tools/v3/assert"
)

func TestOutput(t *testing.T) {
	b, code, err := Output(
		Command("ls", "-la"),
		Command("grep", "process"),
		Command("sed", "s/process/***/g"),
	)

	assert.NilError(t, err)
	assert.Equal(t, 0, code)

	fmt.Println("\nRESULT: ")

	s := string(b)
	fmt.Println(s)
}

func TestOutputPipe(t *testing.T) {
	p, err := Pipe(
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
