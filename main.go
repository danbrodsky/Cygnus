package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"
)

var (
	ClientIpPort string // IP:port of client host is streaming to
	Resolution string // Host screen resolution
	Display string // $DISPLAY environment variable
	Framerate string // frames per second sent to client
	SdpFileName string // spec file for recognizing libx264 format
	StreamToleranceTime time.Duration // time until stream considered disconnected
)

func main() {
	ClientIpPort = "127.0.0.1:1234"
	Resolution = "1920x1080"
	Display = ":0"
	Framerate = "60"
	SdpFileName = "test.sdp"

	go ReceiveHostStream()

	SendStreamToClient()
}

// Host-side function for broadcasting an RTP stream of a screen to the client
func SendStreamToClient() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // calling cancel() kills the exec command
	ffmpegCommand := exec.CommandContext(ctx,"ffmpeg", "-f", "x11grab",
		"-s", Resolution, "-i", Display, "-threads", "6", "-r", Framerate,
		"-vcodec", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf",
		"51", "-b:v", "8000k", "-f", "rtp", "rtp://" + ClientIpPort)
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

	ffplayCommand := exec.CommandContext(ctx,"ffplay", "-protocol_whitelist", "file,udp,rtp", SdpFileName)

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