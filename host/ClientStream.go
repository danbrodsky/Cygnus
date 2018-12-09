package host

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

type ClientStream struct {
	ClientIpPort        string        // IP:port of client host is streaming to
	HostInputIpPort     string        // IP:port of the server on the host that accepts input
	SdpFileName         string        // spec file for recognizing libx264 format
	StreamToleranceTime time.Duration // time until stream considered disconnected
	stopSendingToHost   chan bool     // client signal to stop ongoing goroutines on connection close

	clientErrorReceived chan string   // client error for indicating that host is disconnected
}

func (cs *ClientStream) SendInputToHost() {

	var conn net.Conn
	timeout := time.Now().Add(10 * time.Second)
	for {
		if timeout.Sub(time.Now()) < 0 * time.Second {
			cs.clientErrorReceived <- "could not reach host key event port"
			return
		}
		conn, _ = net.Dial("tcp", cs.HostInputIpPort)
		if conn != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	defer conn.Close()
	c := make(chan InputEvent)
	go cs.grabInput(c)
	for ie := range c {
		conn.Write([]byte(ie.data + "\n"))
	}
}

type InputEvent struct {
	data string
}

func (cs *ClientStream) grabInput(c chan InputEvent) {
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
			case <-cs.stopSendingToHost:
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

// Client-side function for receiving and decoding RTP packets sent by host
func (cs *ClientStream) ReceiveHostStream() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command

	// TODO: add -fs for fullscreen when done testing
	ffplayCommand := exec.CommandContext(ctx, "ffplay", "-protocol_whitelist", "file,udp,rtp", cs.SdpFileName)

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

			if time.Now().Sub(timeOfLastPacket) > cs.StreamToleranceTime {
				// timeout reached, end stream and find new host
				cs.stopSendingToHost <- true
				cs.clientErrorReceived <- "waiting for host stream timed out"
				fmt.Println("END HOST CONNECTION")
				return
			}

			if ReceiveHostData(m) == 1 {
				lostConnection = false
			}
		}

	}
	cs.stopSendingToHost <- true

	ffplayCommand.Wait()
	if err != nil {
		log.Fatal(err)
	}


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

// connect to host stream and start sending key inputs
func (cs *ClientStream) ConnectToHost(clientIpPort string, hostInputIpPort string) {

	cs.ClientIpPort = clientIpPort
	cs.HostInputIpPort = hostInputIpPort
	cs.SdpFileName = "StreamConfig.sdp"

	cs.StreamToleranceTime = 5 * time.Second

	cs.stopSendingToHost = make(chan bool, 2)
	cs.clientErrorReceived = make(chan string, 2)

	go cs.ReceiveHostStream()
	go cs.SendInputToHost()

}

// TODO: Add state controllers for host and client

// TODO: Combine host and client with host network code