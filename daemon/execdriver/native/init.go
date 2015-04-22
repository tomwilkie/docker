// +build linux

package native

import (
	"fmt"
	"os"
	"runtime"

	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/libcontainer"
)

func init() {
	reexec.Register(DriverName, initializer)
}

func fatal(err error) {
	if lerr, ok := err.(libcontainer.Error); ok {
		lerr.Detail(os.Stderr)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func initializer() {
	runtime.GOMAXPROCS(1)
	runtime.LockOSThread()
	factory, err := libcontainer.New("")
	if err != nil {
		fatal(err)
	}

	// I think this is needed to
	os.Setenv("_LIBCONTAINER_INITPIPE", "3")
	if err := factory.StartInitialization(); err != nil {
		fatal(err)
	}

	panic("unreachable")
}

func writeError(err error) {
	fmt.Fprint(os.Stderr, err)
	os.Exit(1)
}
