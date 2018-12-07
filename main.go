package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"time"
)

var (
	ClientIpPort        string        // IP:port of client host is streaming to
	HostInputIpPort     string        // IP:port of the server on the host that accepts input
	Resolution          string        // Host screen resolution
	Display             string        // $DISPLAY environment variable
	Framerate           string        // frames per second sent to client
	SdpFileName         string        // spec file for recognizing libx264 format
	StreamToleranceTime time.Duration // time until stream considered disconnected
)

func main() {
	ClientIpPort = "127.0.0.1:1234"
	HostInputIpPort = "127.0.0.1:1235"
	Resolution = "1920x1080"
	Display = ":0"
	Framerate = "60"
	SdpFileName = "test.sdp"

	go ReceiveHostStream()
	go ReceiveInputFromClient()
	go SendInputToHost()
	SendStreamToClient()
}

// starts a server that accepts input events from clients
func ReceiveInputFromClient() error {
	// listen to incoming tcp connections
	l, err := net.Listen("tcp", HostInputIpPort)
	if err != nil {
		return err
	}
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			println(err)
			return err
		}
		// don't need to start a new goroutine since we shouldn't support concurrent clients anyway
		for {
			line, err := bufio.NewReader(c).ReadString('\n')
			if err != nil {
				println(err)
				return err
			}
			fmt.Printf(line)
		}
	}
}

func SendInputToHost() {
	conn, err := net.Dial("tcp", HostInputIpPort)

	if err != nil {
		println(err)
		return
	}
	c := make(chan InputEvent)
	defer close(c)
	go GrabInput(c)
	for ie := range c {
		conn.Write([]byte(ie.data + "\n"))
	}
}

type InputEvent struct {
	data string
}

func GrabInput(c chan InputEvent) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command
	xevCommand := exec.CommandContext(ctx, "xev", "-display", Display)
	stdout, err := xevCommand.StdoutPipe()
	xevCommand.Start()

	scanner := bufio.NewScanner(stdout)
	var buffer bytes.Buffer
	for scanner.Scan() {
		m := strings.TrimSpace(scanner.Text())
		if m != "" {
			buffer.WriteString(m)
			buffer.WriteString(" ")
		} else {
			c <- InputEvent{data: buffer.String()}
			buffer.Reset()
		}
	}
	xevCommand.Wait()
	if err != nil {
		log.Fatal(err)
	}
}

// Host-side function for broadcasting an RTP stream of a screen to the client
func SendStreamToClient() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command
	ffmpegCommand := exec.CommandContext(ctx, "ffmpeg", "-f", "x11grab",
		"-s", Resolution, "-i", Display, "-threads", "6", "-r", Framerate,
		"-vcodec", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf",
		"51", "-b:v", "8000k", "-f", "rtp", "rtp://"+ClientIpPort)
	stderr, err := ffmpegCommand.StderrPipe()
	ffmpegCommand.Start()

	scanner := bufio.NewScanner(stderr)
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		m := scanner.Text()
		fmt.Println(m)
	}
	ffmpegCommand.Wait()
	if err != nil {
		log.Fatal(err)
	}

}

// Client-side function for receiving and decoding RTP packets sent by host
func ReceiveHostStream() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command

	ffplayCommand := exec.CommandContext(ctx, "ffplay", "-protocol_whitelist", "file,udp,rtp", SdpFileName)

	stderr, err := ffplayCommand.StderrPipe()
	ffplayCommand.Start()

	scanner := bufio.NewScanner(stderr)
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		m := scanner.Text()
		// TODO: failure detection if host stops sending packets
		fmt.Println(m)
	}
	ffplayCommand.Wait()
	if err != nil {
		log.Fatal(err)
	}
}

// Signals to cancel stream if no frame in StreamToleranceTime
func DetectStreamFailure(s string) {

}
