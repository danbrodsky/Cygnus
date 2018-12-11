package main

import (
	"../node"
	"log"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("Usage: go run node.go <param_path> <is_host> ")
	}
	paramsPath := os.Args[1]
	isHost, err := strconv.ParseBool(os.Args[2])
	if err != nil {
		log.Fatal("Second parameter must be a boolean")
	}
	node.Initialize(paramsPath, isHost)
	channel := make(chan bool)
	<- channel
}