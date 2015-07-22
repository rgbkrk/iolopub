package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	juno "github.com/rgbkrk/juno"
)

func main() {
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatalln("Need a connection file.")
	}

	// Expects a runtime kernel-*.json
	connInfo, err := juno.OpenConnectionFile(flag.Arg(0))

	if err != nil {
		fmt.Errorf("%v\n", err)
		os.Exit(1)
	}

	iopub, err := juno.NewIOPubSocket(connInfo, "")

	if err != nil {
		fmt.Errorf("Couldn't start the iopub socket: %v", err)
	}

	defer iopub.Close()

	for {
		message, err := iopub.ReadMessage()

		if err != nil {
			fmt.Errorf("%v\n", err)
			continue
		}

		c, err := json.Marshal(message)
		fmt.Println(string(c))
	}

}
