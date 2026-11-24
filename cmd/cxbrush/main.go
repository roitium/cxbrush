package main

import "os"

func main() {
	os.Exit(runServer(os.Args[1:]))
}
