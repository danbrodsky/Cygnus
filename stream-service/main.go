package main

import (
	"host"
	"log"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) != 1 {
		log.Println("Usage: go run main.go [parameters]")
	} else {
		host.Initialize(args[0])

		channel := make(chan bool)
		<- channel

		//clientLocation := host.Location{Latitude: 49.162781, Longitude: -123.136650}
		//foundHost := host.FindHostForClient(clientLocation)
		//log.Println("Found Host: " + foundHost)
	}
}
