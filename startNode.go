package main

import (
	"./node"
	"bufio"
	"fmt"
	"os"
)

func main(){
	var n *node.Node
	if len(os.Args) != 2 {
		fmt.Println("Invalid input: expected go run startNode.go {node #}, exiting")
		os.Exit(0)
	}
	switch os.Args[1] {
	case "1":
		n = node.Initialize("./parameters/parameters1.json")
	case "2":
		n = node.Initialize("./parameters/parameters2.json")
	case "3":
		n = node.Initialize("./parameters/parameters3.json")
	case "4":
		n = node.Initialize("./parameters/parameters4.json")
	case "5":
		n = node.Initialize("./parameters/parameters5.json")
	case "6":
		n = node.Initialize("./parameters/parameters6.json")
	default:
		fmt.Println("Invalid input: expected go run startNode.go {node #}, exiting")
		os.Exit(0)
	}
	for {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Type 'find host' to find a host or 'view ledger' to view the ledger: ")
		text, _ := reader.ReadString('\n')
		if text == "find host\n" {
			fmt.Println("host search initialized")
			n.FindHostForClient(n.PublicIp + ":1337")
		} else if text == "view ledger\n" {
			fmt.Println("Printing Ledger")
			fmt.Println(n.GetLedger())
		}
	}
}