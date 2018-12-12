package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
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

	hostErrorReceived   chan string // host error for indicating that client is disconnected
	clientErrorReceived chan string // client error for indicating that host is disconnected
)

func main() {
	ClientIpPort = "127.0.0.1:1234"
	HostInputIpPort = "127.0.0.1:1235"
	Resolution = "1280x800"
	Display = ":1"
	Framerate = "60"
	SdpFileName = "StreamConfig.sdp"

	StreamToleranceTime = 5 * time.Second

	ConnectToHost()
	ConnectToClient()
	go CheckClientHostErrors()
	time.Sleep(100 * time.Second)
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
					//fmt.Println(timeout.Sub(time.Now()) )
					if timeout.Sub(time.Now()) < 0*time.Second {
						hostErrorReceived <- "timeout while receiving inputs from client"
						stopSendingToClient <- true
						return
					}
				} else {
					timeout = time.Now().Add(10 * time.Second)
				}
				if line != "" {
					var ie InputEvent
					err := json.Unmarshal([]byte(line), &ie)
					if err != nil {
						fmt.Println("error decoding", line, err)
					}
					fmt.Printf("Received event: %+v\n", ie)
                    var verb string
                    switch ie.Type {
                    case 2:
                        verb = "keydown"
                    case 3:
                        verb = "keyup"
                    case 6:
                        verb = ""
                        exec.Command("xdotool", "mousemove", string(ie.X), string(ie.Y)).Start()
                    case 15:
                        verb = "mousedown"
                    case 16:
                        verb = "mouseup"
                    default:
                        verb = ""
                    }
                    if verb != "" {
                        exec.Command("xdotool", verb, strconv.Itoa(ie.Keycode)).Start()
                    }
				}
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
		if timeout.Sub(time.Now()) < 0*time.Second {
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
		b, err := json.Marshal(&ie)
        if err != nil {
            fmt.Printf("# ERROR MARSHALING # %+v\n", ie)
            println(err.Error())
        }
		conn.Write([]byte(string(b) + "\n"))
	}
}

type InputEvent struct {
	Type    int
	Keycode int
    X int
    Y int
}

func GrabInput(c chan InputEvent) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command
	xevCommand := exec.CommandContext(ctx, "xinput", "test-xi2", "--root")
	xevCommand.Env = os.Environ()
	xevCommand.Env = append(xevCommand.Env, "DISPLAY="+Display)
	stdout, err := xevCommand.StdoutPipe()
	xevCommand.Start()

	scanner := bufio.NewScanner(stdout)
	var current *InputEvent
	for scanner.Scan() {
		select {
		case <-stopSendingToHost:
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
                sendcopy := *current
                fmt.Printf("*** SENDING %+v\n", sendcopy)
				c <- sendcopy
			}
            event_type, _ := strconv.Atoi(fields[2])
            // enter and leave events are useless and interfere for some reason
            if event_type != 7 && event_type != 8 {
                current = &InputEvent{Type: event_type}
            } else {
                current = nil
            }
			//println("event type", current.Type, "starts", m)
		} else {
			if current == nil {
				// haven't seen an event yet so skip ahead until first EVENT
				continue
			}
            //println("event continues", m)
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
                continue
			}
            // only care about valuators for mouse motion events
            if current.Type == 6 && fields[0] == "valuators:" {
                scanner.Scan()
                xfields := strings.Fields(scanner.Text())
                if len(xfields) < 2 {
                    continue // can't parse this event
                }
                x, err := strconv.ParseFloat(xfields[1], 64)
                if err != nil {
                    continue
                }
                current.X = int(x)
                scanner.Scan()
                yfields := strings.Fields(scanner.Text())
                if len(yfields) < 2 {
                    continue // can't parse this event
                }
                y, err := strconv.ParseFloat(yfields[1], 64)
                if err != nil {
                    continue
                }
                current.Y = int(y)
                continue
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
		case err := <-hostErrorReceived:
			fmt.Println(err)
		case err := <-clientErrorReceived:
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
