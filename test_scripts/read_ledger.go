package main

import (
	"fmt"
	"log"
	"../ledger"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: go run read_ledger.go <node_address>")
	}
	nodeAddr := os.Args[1]
	learner := ledger.Initialize(nodeAddr)
	l, err := learner.GetLedger()
	if err != nil {
		log.Println("Error reading ledger")
		log.Println(err)
	} else {
		fmt.Println(l)
	}
}