package easycontainers

import (
	"bytes"
	"errors"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"fmt"
	"os"

	"go/build"
	"net"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
)

const prefix = "easycontainers-"

var (
	getFreePortLock = &sync.Mutex{}
	allocatedPorts  = map[int]struct{}{}
)

func init() {
	// we random numbers for port generation
	rand.Seed(time.Now().UTC().UnixNano())

	// cleanup any oustanding containers with the easycontainers prefix
	CleanupAllContainers()

	WaitForCleanup()

	// cleanup any outstanding sql files in temp
	filepath.Walk(os.TempDir(), func(path string, info os.FileInfo, err error) error {
		if strings.HasPrefix(info.Name(), prefix) {
			os.Remove(path)
		}

		return nil
	})

	// try to cleanup containers if signaled to quit
	signalCh := make(chan os.Signal, 1024)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGKILL)

	go func() {
		<-signalCh

		CleanupAllContainers()
	}()
}

// GoPath returns the value stored in the GOPATH environment variable.
// If that value isn't set in the environment, it will return the value
// of build.Default.GOPATH.
func GoPath() string {
	s, exists := os.LookupEnv("GOPATH")
	if !exists {
		s = build.Default.GOPATH
	}

	return s
}

// CleanupAllContainers will stop all containers starting with prefix
func CleanupAllContainers() error {
	cmd := exec.Command(
		"/bin/bash",
		"-c",
		fmt.Sprintf(`docker stop $(docker ps --filter="name=%s" --format="{{.ID}}")`, prefix),
	)

	var b bytes.Buffer
	cmd.Stderr = &b

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error in command : %s -- %s", err, b.String())
	}

	return err
}

// WaitForCleanup checks every second if there are any easycontainers containers still
// live, and exits when there aren't, or when the timeout occurrs -- whichever comes first
func WaitForCleanup() error {
	cmd := exec.Command(
		"/bin/bash",
		"-c",
		fmt.Sprintf(
			`while [ "$(docker ps --filter="name=%s" --format="{{.ID}}")" ]; do echo 'waiting for cleanup to finish'; sleep 1; done`,
			prefix,
		),
	)

	return RunCommandWithTimeout(cmd, 1*time.Minute)
}

// CleanupContainer stops the container with the specified name.
func CleanupContainer(name string) error {
	cmd := exec.Command(
		"/bin/bash",
		"-c",
		fmt.Sprintf(`docker stop $(docker ps --filter="name=^/%s$" --format="{{.ID}}")`, name),
	)

	var b bytes.Buffer
	cmd.Stderr = &b

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error in command : %s -- %s", err, b.String())
	}

	return err
}

// Logs runs the docker logs command on the specified container and returns the output
func Logs(name string) string {
	cmd := exec.Command(
		"docker",
		"logs",
		name,
	)

	var outputBuf bytes.Buffer
	cmd.Stderr = &outputBuf
	cmd.Stdout = &outputBuf

	cmd.Run()

	return outputBuf.String()
}

// RunCommandWithTimeout will execute the specified cmd, but will timeout and
// return and error after 1 minute.
func RunCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration) error {
	finish := make(chan error)
	timer := time.NewTimer(timeout)

	go func() {
		var err error

		defer func() {
			finish <- err
		}()

		var b bytes.Buffer
		cmd.Stderr = &b
		cmd.Stdout = os.Stdout

		err = cmd.Run()
		if err != nil {
			err = fmt.Errorf("error in command : %s -- %s", err, b.String())
			return
		}
	}()

	select {
	case err := <-finish:
		if err != nil {
			return err
		}
	case <-timer.C:
		return errors.New("container timed out")
	}

	return nil
}

func cmdForContainer(name string, cmd *exec.Cmd) *exec.Cmd {
	return exec.Command(
		"docker",
		"exec",
		name,
		"/bin/bash",
		"-c",
		strings.Join(cmd.Args, " "),
	)
}

func strCmdForContainer(name string, str string) *exec.Cmd {
	return exec.Command(
		"docker",
		"exec",
		name,
		"/bin/bash",
		"-c",
		str,
	)
}

func getFreePort() (int, error) {
	getFreePortLock.Lock()
	defer getFreePortLock.Unlock()

	// because ports that get returned don't connect until the containers are
	// actually started, the same port can get returned for multiple containers
	// which causes issues, so if a port has already been returned at some point,
	// don't return it again, just check for another port up to 10 times
	for i := 0; i < 10; i++ {
		// this block is a code snippet from github.com/phayes/freeport
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return 0, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return 0, err
		}
		defer l.Close()

		port := l.Addr().(*net.TCPAddr).Port
		if _, exists := allocatedPorts[port]; !exists {
			allocatedPorts[port] = struct{}{}

			return port, nil
		}
	}

	return 0, errors.New("took too long to find free port")
}
