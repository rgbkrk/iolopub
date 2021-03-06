package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/rgbkrk/juno"

	fsnotify "gopkg.in/fsnotify.v1"

	"github.com/gorilla/websocket"

	zmq "github.com/zeromq/goczmq"
)

// WatchRuntimes watches for new running kernels. This is a placeholder.
func WatchRuntimes() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	done := make(chan bool)

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				log.Println("event:", event)
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("modified file:", event.Name)
				}
			case err := <-watcher.Errors:
				log.Println("error:", err)
			}
		}
	}()

	runtimeDir, err := RuntimeDir()
	if err != nil {
		return err
	}

	err = watcher.Add(runtimeDir)
	if err != nil {
		return err
	}
	<-done

	return err
}

// HomeDir is the home directory for the user. Light wrapper on the homedir lib.
func HomeDir() (string, error) {
	homish, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("Unable to acquire home directory: %v", err)
	}

	home, err := homedir.Expand(homish)
	if err != nil {
		return "", fmt.Errorf("Unable to expand home directory: %v", err)
	}

	return home, nil
}

// ConfigDir gets the Jupyter config directory for this platform and user.
//
// Returns JUPYTER_CONFIG_DIR if defined, else ~/.jupyter
func ConfigDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}

	if os.Getenv("JUPYTER_CONFIG_DIR") != "" {
		return os.Getenv("JUPYTER_CONFIG_DIR"), nil
	}

	return path.Join(home, ".jupyter"), nil
}

// DataDir gets the config directory for Jupyter data files. These are
// non-transient, non-configuration files.
//
// Returns JUPYTER_DATA_DIR if defined, else a platform-appropriate path.
func DataDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}

	if runtime.GOOS == "darwin" {
		return path.Join(home, "Library", "Jupyter"), nil
	} else if runtime.GOOS == "windows" {
		// Modern Windows
		appdata := os.Getenv("APPDATA")
		if appdata != "" {
			return path.Join(appdata, "jupyter"), nil
		}

		// TODO: jupyter_config_dir() from python
		configDir, err := ConfigDir()
		if err != nil {
			return "", err
		}
		return path.Join(configDir, "data"), nil

	} else { // *nix
		xdg := os.Getenv("XDG_DATA_HOME")
		if xdg != "" {
			xdg = path.Join(home, ".local", "share")
		}
		return path.Join(xdg, "jupyter"), nil
	}

}

// IsNixButNotDarwin returns true if on *nix (but not on OS X)
func IsNixButNotDarwin() bool {
	return runtime.GOOS == "linux" ||
		runtime.GOOS == "dragonfly" || // So many BSDs
		runtime.GOOS == "freebsd" ||
		runtime.GOOS == "netbsd" ||
		runtime.GOOS == "openbsd" ||
		runtime.GOOS == "solaris"
}

// RuntimeDir is the directory of running kernels
func RuntimeDir() (string, error) {
	if os.Getenv("JUPYTER_RUNTIME_DIR") != "" {
		return os.Getenv("JUPYTER_RUNTIME_DIR"), nil
	}

	if IsNixButNotDarwin() && os.Getenv("XDG_RUNTIME_DIR") != "" {
		return path.Join(os.Getenv("XDG_RUNTIME_DIR"), "jupyter"), nil
	}

	dataDir, err := DataDir()
	if err != nil {
		return "", err
	}

	return path.Join(dataDir, "runtime"), nil

}

func main() {
	connFile := flag.String("existing", "", "Path to connection file")
	ioloHub := flag.String("hub", "http://iolo.fict.io:80", "IOLO Hub Base URL")
	flag.Parse()

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Println()
	}

	if *connFile == "" {
		flag.Usage()
		log.Fatalln("Need a connection file.")
	}

	// Expects a runtime kernel-*.json
	connInfo, err := juno.NewConnectionInfo(*connFile)

	if err != nil {
		log.Fatalf("%v\n", err)
	}

	u, err := url.Parse(*ioloHub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to parse URL: %v", err)
		os.Exit(3)
	}

	u.Path = "/ws"

	rawConn, err := net.Dial("tcp", u.Host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to host: %v", err)
		os.Exit(4)
	}

	wsHeaders := http.Header{
		"Origin":                   {u.Scheme + "://" + u.Host},
		"Sec-WebSocket-Extensions": {"permessage-deflate; client_max_window_bits, x-webkit-deflate-frame"},
	}

	writeBufferSize := 1024 * 1024 * 2

	wsConn, resp, err := websocket.NewClient(rawConn, u, wsHeaders, 0, writeBufferSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "websocket.NewClient Error: %s\nResp:%+v", err, resp)
		os.Exit(5)
	}
	defer wsConn.Close()

	ioConnection := connInfo.IOPubConnectionString()
	iopub := zmq.NewSubChanneler(ioConnection, "")
	defer iopub.Destroy()

	for {
		select {
		case wireMessage := <-iopub.RecvChan:
			var message juno.Message
			err := message.ParseWireProtocol(wireMessage, connInfo)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Unable to read message %v\n", err)
				continue
			}
			err = wsConn.WriteJSON(message)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Problem writing JSON %v.\nOutput too big?", err)
				break
			}
		}
	}

}
