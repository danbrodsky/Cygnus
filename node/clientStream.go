package node

import (
	"bufio"
    "strconv"
	"context"
    "encoding/json"
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
		b, _ := json.Marshal(&ie)
		conn.Write(b)
		conn.Write([]byte("\n"))
	}
}

type InputEvent struct {
	Type    int
	Keycode int
}

func (cs *ClientStream) grabInput(c chan InputEvent) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command
	xevCommand := exec.CommandContext(ctx, "xinput", "test-xi2", "--root")
	stdout, err := xevCommand.StdoutPipe()
	xevCommand.Start()

	scanner := bufio.NewScanner(stdout)
    var current *InputEvent
	for scanner.Scan() {
		select {
		case <-cs.stopSendingToHost:
			fmt.Println("STOP SENDING TO HOST")
			close(c)
			return
		default:
		}
		m := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(m)

		if len(fields) > 0 && fields[0] == "EVENT" {
			// new event starts => send old and reset
			if current != nil {
				c <- *current
			}
			current = &InputEvent{}
			current.Type, _ = strconv.Atoi(fields[2])
			//println("event starts", m)
		} else {
			if current == nil {
				// haven't seen an event yet so skip ahead until first EVENT
				continue
			}
			if m == "" {
				// blank line => send what we have
				if current != nil {
					c <- *current
				}
				current = &InputEvent{}
				continue
			}
			if fields[0] == "detail:" {
				current.Keycode, _ = strconv.Atoi(fields[1])
			}
			//println("event continues", m)

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
	ffplayCommand := exec.CommandContext(ctx, "ffplay", "rtp://"+cs.ClientIpPort)

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
	ffplayCommand.Wait()
	cs.clientErrorReceived <-"ffplay command stopped"
		cs.stopSendingToHost <- true
	if err != nil {
		log.Fatal(err)
		fmt.Println(err)
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

	fmt.Println("Connecting to host at: " + hostInputIpPort)
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
