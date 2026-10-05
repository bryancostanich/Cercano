// fixture-agent is a stand-in for the real Windows cercano.exe. It is built
// from the original spike's OWN source (never the production Cercano code) and is
// test scaffolding only: it exists so the experiments can observe real OS
// behavior (running, locking, replacing processes) without touching any real
// Cercano binary.
//
// Commands:
//
//	--version           print the version this binary was built with
//	--health            exit 0 unless built with -X main.unhealthy=true
//	--hold-open         keep running until SIGTERM/SIGINT, holding the
//	                    executable file open via os.Executable (models the
//	                    agent running as a long-lived service)
//	--hold-file FILE    open and hold FILE open until SIGTERM (models an
//	                    updater trying to replace a file an old process keeps
//	                    open)
package main

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// Set by -ldflags "-X main.version=..." when the fixture is built.
var (
	version   = "0.0.0-dev"
	unhealthy = "false"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "--health":
		if unhealthy == "true" {
			fmt.Fprintln(os.Stderr, "unhealthy build")
			os.Exit(1)
		}
		fmt.Println("ok")
		os.Exit(0)
	case "--hold-open":
		holdOpen()
	case "--hold-file":
		if len(os.Args) < 3 {
			usage()
		}
		holdFile(os.Args[2])
	default:
		usage()
	}
}

// holdOpen keeps this process alive and prints the resolved executable path
// plus the OS, so tests can (a) confirm the file being locked and (b) reason
// about replacement while running. It exits 0 on SIGTERM/SIGINT.
func holdOpen() {
	exe, err := os.Executable()
	if err != nil {
		exe = fmt.Sprintf("<os.Executable error: %v>", err)
	}
	// Real handle: reading our own binary keeps an open fd on it for the
	// lifetime of the process.
	f, ferr := os.Open(exe)
	defer func() {
		if ferr == nil {
			f.Close()
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("pid=%d exe=%s os=%s\n", os.Getpid(), exe, runtime.GOOS)
	if ferr != nil {
		fmt.Printf("open-self-error=%v\n", ferr)
	} else {
		buf := make([]byte, 1)
		_, _ = f.ReadAt(buf, 0) // touch it so the fd is genuinely in use
	}
	<-stop
	fmt.Println("bye")
	os.Exit(0)
}

// holdFile opens FILE and holds it open until signaled, printing "ready" so
// tests can synchronize.
func holdFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
		os.Exit(1)
	}
	defer f.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("ready")
	<-stop
	os.Exit(0)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fixture-agent --version | --health | --hold-open | --hold-file FILE")
	os.Exit(2)
}
