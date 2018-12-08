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
	stopSendingToHost   chan bool     // client signal to stop ongoing goroutines on connection close
	stopSendingToClient chan bool     // host signal to stop ongoing goroutines on connection close

	hostErrorReceived   chan string   // host error for indicating that client is disconnected
	clientErrorReceived chan string   // client error for indicating that host is disconnected
)

func main() {
	ClientIpPort = "127.0.0.1:1234"
	HostInputIpPort = "127.0.0.1:1235"
	Resolution = "1920x1080"
	Display = ":0"
	Framerate = "60"
	SdpFileName = "StreamConfig.sdp"

	StreamToleranceTime = 5 * time.Second

	ConnectToHost()
	ConnectToClient()
	go CheckClientHostErrors()
	time.Sleep(100*time.Second)
}

// starts a server that accepts input events from clients
func ReceiveInputFromClient() {
	// listen to incoming tcp connections
	l, err := net.Listen("tcp", HostInputIpPort)
	if err != nil {
		stopSendingToClient <- true
		hostErrorReceived <- err.Error()
		return
	}
	defer l.Close()

	conns := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		conns <- c
	}()

	select {
	case c := <-conns:
		timeout := time.Now().Add(10 * time.Second)
		for {
			select {
			case <-stopSendingToClient:
				fmt.Println("input connection with client closed")
				return
			default:
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					fmt.Println(timeout.Sub(time.Now()) )
					if timeout.Sub(time.Now()) < 0 * time.Second {
						hostErrorReceived <- "timeout while receiving inputs from client"
						stopSendingToClient <- true
						return
					}
				} else { timeout = time.Now().Add(10 * time.Second) }
				fmt.Printf(line)
			}
		}
	case <-time.After(10 * time.Second):
		stopSendingToClient <- true
		hostErrorReceived <- "timed out waiting for client to connect"
		return
	}
}

func SendInputToHost() {

	var conn net.Conn
	timeout := time.Now().Add(10 * time.Second)
	for {
		if timeout.Sub(time.Now()) < 0 * time.Second {
			clientErrorReceived <- "could not reach host key event port"
			return
		}
		conn, _ = net.Dial("tcp", HostInputIpPort)
		if conn != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	defer conn.Close()
	c := make(chan InputEvent)
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
	xevCommand := exec.CommandContext(ctx, "xinput", "test-xi2", "--root")
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
			select {
			case <-stopSendingToHost:
				fmt.Println("STOP SENDING TO HOST")
				close(c)
				return
			default:
				c <- InputEvent{data: buffer.String()}
				buffer.Reset()
			}
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
		"-s", Resolution, "-i", Display, "-threads", "4", "-r", Framerate,
		"-vcodec", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf",
		"51", "-b:v", "8000k", "-f", "rtp", "rtp://"+ClientIpPort)
	_, err := ffmpegCommand.StderrPipe()
	ffmpegCommand.Start()
	select {
	case <-stopSendingToClient:
		return // TODO: check if this works
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

	// TODO: add -fs for fullscreen when done testing
	ffplayCommand := exec.CommandContext(ctx, "ffplay", "-protocol_whitelist", "file,udp,rtp", SdpFileName)

	stderr, err := ffplayCommand.StderrPipe()
	ffplayCommand.Start()

	scanner := bufio.NewScanner(stderr)
	scanner.Split(bufio.ScanWords)

	timeOfLastPacket := time.Now()
	lostConnection := true
	for scanner.Scan() {
		m := scanner.Text()
		if !lostConnection {
			if ReceiveHostData(m) == 0 {
				timeOfLastPacket = time.Now()
				lostConnection = true
			}
		} else {

			if time.Now().Sub(timeOfLastPacket) > StreamToleranceTime {
				// timeout reached, end stream and find new host
				stopSendingToHost <- true
				clientErrorReceived <- "waiting for host stream timed out"
				fmt.Println("END HOST CONNECTION")
				return
			}

			if ReceiveHostData(m) == 1 {
				lostConnection = false
			}
		}

	}
	stopSendingToHost <- true

	ffplayCommand.Wait()
	if err != nil {
		log.Fatal(err)
	}


}

// get new host for client
func FindNewHost() {

}

// test function for checking error messages
func CheckClientHostErrors() {
	for {
		select {
		case err := <- hostErrorReceived:
			fmt.Println(err)
		case err:= <- clientErrorReceived:
			fmt.Println(err)
		}
	}
}

// connect to host stream and start sending key inputs
func ConnectToHost() {
	stopSendingToHost = make(chan bool, 2)
	clientErrorReceived = make(chan string)

	go ReceiveHostStream()
	go SendInputToHost()

}

func ConnectToClient() {
	stopSendingToClient = make(chan bool, 2)
	hostErrorReceived = make(chan string)

	go ReceiveInputFromClient()
	go SendStreamToClient()
}

// Signals to cancel stream if no frame in StreamToleranceTime

var videoPacket = false
func ReceiveHostData(s string) int {
	// 0 if 0kB video packet, 1 if NkB video packet, -1 otherwise

	if videoPacket {
		videoPacket = false
		if s == "0KB" {
			return 0
		}
		return 1
	}
	if s == "vq=" {
		videoPacket = true
	}

	return -1
}

// TODO: Add state controllers for host and client
// TODO: Combine host and client with host network code