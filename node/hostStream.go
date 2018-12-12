package node

import (
	"bufio"
	"context"
    "encoding/json"
	"os"
	"strconv"
	"fmt"
	"log"
	"net"
	"os/exec"
	"time"
)

//TODO: made short for testing. Increases later for demo
const LedgerInterval = time.Minute * 5

type HostStream struct {
	ClientIpPort        string        // IP:port of client host is streaming to
	HostInputIpPort     string        // IP:port of the server on the host that accepts input
	Resolution          string        // Host screen resolution
	Display             string        // $DISPLAY environment variable
	Framerate           string        // frames per second sent to client
	StreamToleranceTime time.Duration // time until stream considered disconnected
	stopSendingToClient chan bool     // host signal to stop ongoing goroutines on connection close
	hostErrorReceived   chan string   // host error for indicating that client is disconnected
	logStreamTime		chan LedgerEntry
}

// starts a server that accepts input events from clients
func (hs *HostStream) ReceiveInputFromClient() {
	// listen to incoming tcp connections
	l, err := net.Listen("tcp", hs.HostInputIpPort)
	if err != nil {
		hs.stopSendingToClient <- true
		hs.hostErrorReceived <- err.Error()
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
		timeout := time.Now().Add(hs.StreamToleranceTime)

		//Start logging time
		startTime := time.Now()
		for {
			select {
			case <-hs.stopSendingToClient:
				fmt.Println("input connection with client closed")

				//Log time on connection closed
				entry := LedgerEntry{ClientId: hs.ClientIpPort, StartTime: startTime, EndTime: time.Now()}
				hs.logStreamTime <- entry
				return
			default:
				//Log time after a certain interval
				if time.Now().After(startTime.Add(LedgerInterval)) {
					endTime := time.Now()
					entry := LedgerEntry{ClientId: hs.ClientIpPort, StartTime: startTime, EndTime: endTime}
					startTime = endTime
					hs.logStreamTime <- entry
				}

				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					if timeout.Sub(time.Now()) < 0 * time.Second {
						hs.hostErrorReceived <- "timeout while receiving inputs from client"
						hs.stopSendingToClient <- true

						//Log time on input timeout
						entry := LedgerEntry{ClientId: hs.ClientIpPort, StartTime: startTime, EndTime: time.Now()}
						hs.logStreamTime <- entry
						return
					}
				} else { timeout = time.Now().Add(hs.StreamToleranceTime) }
				if line != "" {
					var ie InputEvent
					err := json.Unmarshal([]byte(line), &ie)
					if err != nil {
						fmt.Println("error decoding", line, err)
						continue
					}
					//fmt.Printf("Received event: %+v\n", ie)
                    var verb string
                    switch ie.Type {
                    case 2:
                        verb = "keydown"
                    case 3:
                        verb = "keyup"
                    case 6:
                        verb = ""
                        if ie.X != 0 && ie.Y != 0 {
							fmt.Println(strconv.Itoa(ie.X), strconv.Itoa(ie.Y))
							exec.Command("xdotool", "mousemove", strconv.Itoa(ie.X), strconv.Itoa(ie.Y)).Start()
						}
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
	case <-time.After(hs.StreamToleranceTime):
		hs.stopSendingToClient <- true
		hs.hostErrorReceived <- "timed out waiting for client to connect"
		return
	}
}

// Host-side function for broadcasting an RTP stream of a screen to the client
func (hs *HostStream) SendStreamToClient() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command
	ffmpegCommand := exec.CommandContext(ctx, "ffmpeg", "-f", "x11grab",
		"-s", hs.Resolution, "-i", hs.Display, "-threads", "4", "-r", hs.Framerate,
		"-vcodec", "mpeg2video", "-preset", "ultrafast", "-tune", "zerolatency", "-crf",
		"51", "-b:v", "8000k", "-f", "rtp", "rtp://"+hs.ClientIpPort)
	_, err := ffmpegCommand.StderrPipe()
	ffmpegCommand.Start()
	select {
	case <-hs.stopSendingToClient:
		return
	}
	ffmpegCommand.Wait()
	fmt.Println("Host stream has stopped")
	if err != nil {
		log.Fatal(err)
	}
}


func (hs *HostStream) ConnectToClient(clientIpPort string, hostInputIpPort string) {
	hs.stopSendingToClient = make(chan bool, 2)
	hs.hostErrorReceived = make(chan string, 2)

	hs.ClientIpPort = clientIpPort
	hs.HostInputIpPort = hostInputIpPort
	hs.Resolution = "1920x1080"
	hs.Display = os.Getenv("DISPLAY")
	hs.Framerate = "60"

	hs.StreamToleranceTime = 5 * time.Second

	go hs.ReceiveInputFromClient()
	go hs.SendStreamToClient()
}