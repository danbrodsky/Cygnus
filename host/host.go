package host

import (
	"encoding/json"
	"github.com/DistributedClocks/GoVector/govec"
	"github.com/DistributedClocks/GoVector/govec/vrpc"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
)

const MaxTimeouts = 3

type Host struct {
	hostID string

	//Location of the host
	location Location

	//For other hosts to connect using RPC
	privateAddrRPC string
	publicAddrRPC  string

	//For other hosts to connect using UDP
	privateAddrUDP string
	publicAddrUDP  string

	//Used to connect to client
	privateAddrClient string
	publicAddrClient  string

	//Peer ip addresses
	peers map[string]*rpc.Client
	peerLock sync.Mutex

	//Client address. An empty string if there is no client
	clientAddr string
	clientConnected bool
	clientLock sync.Mutex

	//Seen host requests (prevent endless looping during flooding)
	seenHostRequests map[HostRequest]bool
	seenHostRequestsLock sync.Mutex

	//Seen host-client pairings (prevent endless looping during flooding)
	seenPairings map[HostClientPair]bool
	seenPairingsLock sync.Mutex

	//Sequence number for hostRequest messages being flooded
	currSeqNumberHR uint64
	seqNumberLockHR sync.Mutex

	//Sequence number for host-client pair messages being flooded
	currSeqNumberHC uint64
	seqNumberLockHC sync.Mutex

	//Used to wait for ack responses to the client-host pairs sent out by this host
	ackWaitChan map[uint64]chan bool
	seenPairAcks map[PairAck] bool
	pairAcksLock  sync.Mutex

	//Failure detection between hosts
	failureDetectorLock sync.Mutex
	receivedAcks map[string]map[uint64]*Ack
	peerTimeoutsLock sync.Mutex
	peerTimeouts map[string]uint64

	govecLogger *govec.GoLog
}

type HostInterface interface {
	ReceiveHeartBeat(hb *HeartBeat, reply *int) error
	ReceiveACK(ack *Ack, reply *int) error
	RpcAddPeer(ip string, reply *int) error
	ReceivePairAck(pair PairAck, reply *int) error
	ReceivePair(pair HostClientPair, reply *int) error
	ReceiveHostRequest(args HostRequestWithSender, reply *int) error
	setUpMessageRPC()
	notifyPeers()
	addPeer(ip string)
	floodPairAck(pairAck PairAck)
	floodHostRequest(sender string, hostRequest HostRequest)
	floodHostClientPair(pair HostClientPair)
	sendHostClientPair(clientAddr string, hostAddr string) bool
	FindHostForClient(clientAddr string, clientLocation Location) string
	monitorNode(peer string)
	waitForBestHost(addr string, clientLocation Location, seqNum uint64) (map[string] bool, string)
}

var(
	GovecOptions = govec.GetDefaultLogOptions()
)

//Receive a heart beat from another host
func (h *Host) ReceiveHeartBeat(hb *HeartBeat, reply *int) error {
	//log.Println(h.publicAddrRPC + " Received HeartBeat from " + hb.Sender + " Seq num: " + strconv.Itoa(int(hb.SeqNum)))

	h.failureDetectorLock.Lock()
	defer h.failureDetectorLock.Unlock()

	ack := &Ack{HBeatSeqNum: hb.SeqNum, Sender: h.publicAddrRPC}
	//log.Println(h.publicAddrRPC + " Sending ACK to " + hb.Sender + " Seq num: " + strconv.Itoa(int(ack.HBeatSeqNum)))

	client, ok := h.peers[hb.Sender]
	if ok {
		var reply int
		client.Go("Host.ReceiveACK", ack, &reply, nil)
	}
	return nil
}

//Receive an ack from another host
func (h *Host) ReceiveACK(ack *Ack, reply *int) error {
	//log.Println(h.publicAddrRPC + " Received ACK from " + ack.Sender + " Seq num: " + strconv.Itoa(int(ack.HBeatSeqNum)))

	h.failureDetectorLock.Lock()
	defer h.failureDetectorLock.Unlock()
	h.receivedAcks[ack.Sender][ack.HBeatSeqNum] = ack
	return nil
}


//Add peer to this host's peer list
func (h *Host) RpcAddPeer(ip string, reply *int) error {
	h.addPeer(ip)
	return nil
}

//Receive an acknowledgement for a client-host pairing
func (h *Host) ReceivePairAck(pairAck PairAck, reply *int) error {
	h.pairAcksLock.Lock()
	defer h.pairAcksLock.Unlock()

	_, ok := h.seenPairAcks[pairAck]
	if !ok {
		if pairAck.TargetHost == h.publicAddrRPC {
			h.ackWaitChan[pairAck.SequenceNumber] <- pairAck.Accept
		} else {
			h.floodPairAck(pairAck)
		}
		h.seenPairAcks[pairAck] = true
	}
	return nil
}

//Receive a client-host pairing
func (h *Host) ReceivePair(pair HostClientPair, reply *int) error {
	h.seenPairingsLock.Lock()
	_, ok := h.seenPairings[pair]
	h.seenPairings[pair] = true
	h.seenPairingsLock.Unlock()

	//If we haven't seen the message before, check if we are the host that is being paired
	if !ok {
		if pair.Host == h.publicAddrClient {
			h.clientLock.Lock()
			var pairAck PairAck

			//Only accept if we are currently available
			if h.clientAddr == "" {
				h.clientAddr = pair.Client
				h.clientConnected = false

				//Will send an ack indicating that the pairing was accepted
				pairAck = PairAck{
					SequenceNumber: pair.SequenceNumber,
					TargetHost: pair.SendingHost,
					Accept: true,
				}
			} else {
				//Will send an ack indicating that the pairing was rejected
				pairAck = PairAck{
					SequenceNumber: pair.SequenceNumber,
					TargetHost: pair.SendingHost,
					Accept: false,
				}
			}
			h.clientLock.Unlock()

			//Flood the ack to everyone and mark it as seen
			h.pairAcksLock.Lock()
			h.seenPairAcks[pairAck] = true
			h.pairAcksLock.Unlock()
			go h.floodPairAck(pairAck)

			//If no client connects after 1 minute, reset the client address
			//Don't wait forever for the client
			go func() {
				time.Sleep(1 * time.Minute)
				h.clientLock.Lock()
				if !h.clientConnected {
					h.clientAddr = ""
				}
				h.clientLock.Unlock()
			}()
		} else {
			//Flood pairing to peers
			h.floodHostClientPair(pair)
		}
	}
	return nil
}

func (h *Host) ReceiveHostRequest(args HostRequestWithSender, reply *int) error {
	hostRequest := args.Request

	h.seenHostRequestsLock.Lock()
	_, ok := h.seenHostRequests[hostRequest]
	h.seenHostRequests[hostRequest] = true
	h.seenHostRequestsLock.Unlock()

	if !ok {
		h.clientLock.Lock()
		available := h.clientAddr == ""
		h.clientLock.Unlock()

		sendResponse := func(isBest bool) {
			messageToSend := hostRequest
			if isBest {
				messageToSend.BestHost = h.publicAddrClient
				messageToSend.BestHostLocation = h.location
				hostRequest = messageToSend
			} else {
				messageToSend.BestHost = ""
				messageToSend.BestHostLocation = Location{}
			}
			messageToSend.RespondingHost = h.hostID
			conn, err := net.Dial("udp", hostRequest.RequestingHost)
			if err == nil {
				b, err := marshallHostRequest(messageToSend)
				if err == nil {
					conn.Write(b)
				}
			} else {
				log.Println(err)
			}
		}

		//If this host is available and is better than the host in the current request, send itself to the requesting host
		if available {
			//If there is no host in the request right now
			if hostRequest.BestHost == "" {
				sendResponse(true)
			} else {
				currentBestDistance := distance(hostRequest.ClientLocation, hostRequest.BestHostLocation)
				hostDistance := distance(hostRequest.ClientLocation, h.location)
				if hostDistance < currentBestDistance {
					sendResponse(true)
				} else {
					sendResponse(false)
				}
			}
		} else {
			sendResponse(false)
		}
		h.floodHostRequest(args.Sender, hostRequest)
	}
	return nil
}

//Handle RPC messages
func (h *Host) setUpMessageRPC() {
	handler := rpc.NewServer()
	handler.Register(h)
	l, e := net.Listen("tcp", h.privateAddrRPC)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go vrpc.ServeRPCConn(handler, l, h.govecLogger, GovecOptions)
}

//Notify peers that this host has joined
func (h *Host) notifyPeers() {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()
	for peer := range h.peers {
		var reply int
		client, ok := h.peers[peer]
		if ok {
			client.Go("Host.RpcAddPeer", h.publicAddrRPC, reply, nil)
		}
	}
}

//Add a peer to the peers list
func (h *Host) addPeer(ip string) {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()

	_, ok := h.peers[ip]
	if !ok {
		client, err := vrpc.RPCDial("tcp", ip, h.govecLogger, GovecOptions)
		if err == nil {
			h.peers[ip] = client
			go h.monitorNode(ip)
		} else {
			log.Println(err)
		}
	}
}

//Send the pair ack to all peers
func (h *Host) floodPairAck(pairAck PairAck) {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()

	for _, client := range h.peers {
		var reply int
		client.Go("Host.ReceivePairAck", pairAck, reply, nil)
	}
}

//Send the host request to all peers
func (h *Host) floodHostRequest(sender string, hostRequest HostRequest) {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()
	for peer, client := range h.peers {
		if peer != sender {
			var reply int
			hostRequestWithSender := HostRequestWithSender{
				Sender: h.publicAddrRPC,
				Request: hostRequest}
			client.Go("Host.ReceiveHostRequest", hostRequestWithSender, reply, nil)
		}
	}
}

//Send the client host pair to all peers
func (h *Host) floodHostClientPair(pair HostClientPair) {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()

	for _, client := range h.peers {
		var reply int
		client.Go("Host.ReceivePair", pair, reply, nil)
	}
}

//After consensus is done for the client-host pairing, call this function to send the pairing to all hosts
func (h *Host) sendHostClientPair(clientAddr string, hostAddr string) bool {
	h.seqNumberLockHC.Lock()
	seqNum := h.currSeqNumberHC
	h.currSeqNumberHC++
	h.seqNumberLockHC.Unlock()

	//Create the client host pair to flood to the network & mark it as seen
	pair := HostClientPair{
		SequenceNumber: seqNum,
		Client: clientAddr,
		Host: hostAddr,
		SendingHost: h.publicAddrRPC}
	h.seenPairingsLock.Lock()
	h.seenPairings[pair] = true
	h.seenPairingsLock.Unlock()

	//Make a wait chan, so we can wait until the client host pair has be acked by the chosen host
	h.pairAcksLock.Lock()
	waitChan := make(chan bool, 2)
	h.ackWaitChan[seqNum] = waitChan
	h.pairAcksLock.Unlock()

	//Only wait five seconds for an acknowledgement (in case other host has failed)
	go func() {
		time.Sleep(5 * time.Second)
		waitChan <- false
	}()

	//Flood pair to the network, then wait for acknowledgement
	go h.floodHostClientPair(pair)
	return <- waitChan
}

//Will either return the ip of the best host, or an empty string if there are no hosts
//TODO: this method probably doesn't need to be capitalized. Only is capitalize right now for testing purposes
func (h *Host) FindHostForClient(clientAddr string, clientLocation Location) string {
	//Get the sequence number to use
	h.seqNumberLockHR.Lock()
	seqNum := h.currSeqNumberHR
	h.currSeqNumberHR++
	h.seqNumberLockHR.Unlock()

	//Create the host request and mark it as seen
	hostRequest := HostRequest{
		SequenceNumber: seqNum,
		ClientLocation: clientLocation,
		RequestingHost: h.publicAddrUDP}
	h.seenHostRequestsLock.Lock()
	h.seenHostRequests[hostRequest] = true
	h.seenHostRequestsLock.Unlock()

	//Flood to network then wait for responses
	h.floodHostRequest(h.publicAddrRPC, hostRequest)
	respondedHosts, bestHost := h.waitForBestHost(h.privateAddrUDP, clientLocation, seqNum)

	log.Println(respondedHosts) //Array of host IDs that responded to the flooded request

	//TODO: Check that there actually is a best host (bestHost != "")
	//TODO: Do consensus before we send the best client-host pair to everyone

	//Flood the best client-host pairing
	accept := h.sendHostClientPair(clientAddr, bestHost)
	if accept {
		log.Println("Host: " + bestHost + " has agreed to accept client " + clientAddr)
	} else {
		log.Println("Host: " + bestHost + " has won't accept client " + clientAddr)
	}
	//TODO: Do something with the return of 'accept'. If the target host doesn't accept, find another one?

	return bestHost
}

//Wait for hosts to respond. Then choose the best host
func (h *Host) waitForBestHost(addr string, clientLocation Location, seqNum uint64) (map[string] bool, string) {
	bestHost := ""
	bestHostLocation := Location{}
	currentBestDistance := float64(0)

	//If this host has no clients, add the host as the best host
	h.clientLock.Lock()
	if h.clientAddr == "" {
		bestHost = h.publicAddrClient
		bestHostLocation = h.location
		currentBestDistance = distance(clientLocation, h.location)
	}
	h.clientLock.Unlock()

	//Wait for other hosts to send themselves on the udp connection
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

	//Only wait for three seconds
	timeoutTime := time.Now().Add(time.Second * 3)
	l.SetReadDeadline(timeoutTime)

	respondedHosts := make(map[string] bool)

	for time.Now().Before(timeoutTime) {
		buffer := make([]byte, 1024)
		n, _, err := l.ReadFromUDP(buffer)
		if err == nil {
			hostRequest, err := unMarshallHostRequest(buffer, n)
			log.Println(hostRequest)
			h.govecLogger.LogLocalEvent(": Location info from:" + hostRequest.RequestingHost, GovecOptions)
			if err == nil && hostRequest.SequenceNumber == seqNum {
				//If there is currently no best host, set the received host as the best host
				if bestHostLocation == (Location{}) && hostRequest.BestHost != "" {
					bestHost = hostRequest.BestHost
					bestHostLocation = hostRequest.BestHostLocation
					currentBestDistance = distance(clientLocation, bestHostLocation)
				} else if bestHostLocation != (Location{}) && hostRequest.BestHost != "" {
					//Otherwise, check to see if the newly received host is better than the current best host
					newDistance := distance(clientLocation, hostRequest.BestHostLocation)
					if newDistance < currentBestDistance {
						bestHost = hostRequest.BestHost
						bestHostLocation = hostRequest.BestHostLocation
						currentBestDistance = distance(clientLocation, bestHostLocation)
					}
				}
				respondedHosts[hostRequest.RespondingHost] = true
			}
		}
	}

	return respondedHosts, bestHost
}

//Monitor a host node
func (h *Host) monitorNode(peer string) {
	log.Println(h.hostID + " - beginning to monitor " + peer)

	seqNum := uint64(0)
	h.peerTimeoutsLock.Lock()
	h.peerTimeouts[peer] = 0
	h.peerTimeoutsLock.Unlock()

	h.failureDetectorLock.Lock()
	h.receivedAcks[peer] = make(map[uint64]*Ack)
	h.failureDetectorLock.Unlock()

	h.peerLock.Lock()
	client, ok := h.peers[peer]
	h.peerLock.Unlock()
	for ok {
			var reply int
			hb := &HeartBeat{SeqNum: seqNum, Sender: h.publicAddrRPC}

			//log.Println(h.publicAddrRPC + " Sending Heartbeat to " + peer + " Seq num: " + strconv.Itoa(int(hb.SeqNum)))
			client.Go("Host.ReceiveHeartBeat", hb, &reply, nil)
			time.Sleep(1 * time.Second)

			h.failureDetectorLock.Lock()
			_, ok := h.receivedAcks[peer][seqNum]
			if ok {
				h.peerTimeoutsLock.Lock()
				h.peerTimeouts[peer] = 0
				h.peerTimeoutsLock.Unlock()
			} else {
				h.peerTimeoutsLock.Lock()
				timeoutCount := h.peerTimeouts[peer] + 1
				h.peerTimeouts[peer] = timeoutCount
				h.peerTimeoutsLock.Unlock()
				if timeoutCount >= MaxTimeouts {
					h.failureDetectorLock.Unlock()
					break
				}
			}
			h.failureDetectorLock.Unlock()
			seqNum += 1
	}
	h.peerLock.Lock()
	log.Println(h.hostID + "- peer has failed: " + peer)
	delete(h.peers, peer)
	h.peerLock.Unlock()
}

//Calculate distance between two coordinates
func distance(location1 Location, location2 Location) float64 {
	radiansLat1 := toRadians(location1.Latitude)
	radiansLat2 := toRadians(location2.Latitude)
	diffLat := toRadians(location2.Latitude - location1.Latitude)
	diffLong := toRadians(location2.Longitude - location1.Longitude)

	var a = math.Sin(diffLat/2)*math.Sin(diffLat/2) + math.Cos(radiansLat1)*math.Cos(radiansLat2)*math.Sin(diffLong/2)*math.Sin(diffLong/2)
	return 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func toRadians(value float64) float64 {
	return value * math.Pi / 180
}

func concatIp(ip string, port string) string {
	return ip + ":" + port
}

func handleVerificationRequests(verificationAddrPort string){
	//Wait for other hosts to send verification requests for client on the udp connection
}

func Initialize(paramsPath string) (*Host) {
	var params Parameters
	jsonFile, err := os.Open(paramsPath)
	if err != nil {
		log.Println(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	json.Unmarshal(byteValue, &params)
	log.Println(params)

	h := &Host{}
	h.hostID = params.HostID
	h.privateAddrRPC = concatIp(params.HostPrivateIP, params.HostsPortRPC)
	h.publicAddrRPC = concatIp(params.HostPublicIP, params.HostsPortRPC)
	h.privateAddrUDP = concatIp(params.HostPrivateIP, params.HostsPortUDP)
	h.publicAddrUDP = concatIp(params.HostPublicIP, params.HostsPortUDP)
	h.privateAddrClient = concatIp(params.HostPrivateIP, params.AcceptClientsPort)
	h.publicAddrClient = concatIp(params.HostPublicIP, params.AcceptClientsPort)
	h.location = Location{Latitude: params.HostLatitude, Longitude: params.HostLongitude}

	h.govecLogger = govec.InitGoVector(params.HostID, "./logs/" + params.HostID, govec.GetDefaultConfig())

	h.peers = make(map[string]*rpc.Client)
	h.peerLock = sync.Mutex{}
	h.clientLock = sync.Mutex{}

	h.seenHostRequests = make(map[HostRequest]bool)
	h.seenHostRequestsLock = sync.Mutex{}
	h.seenPairings = make(map[HostClientPair]bool)
	h.seenPairingsLock = sync.Mutex{}

	h.currSeqNumberHR = 0
	h.seqNumberLockHR = sync.Mutex{}
	h.currSeqNumberHC = 0
	h.seqNumberLockHC = sync.Mutex{}

	h.failureDetectorLock = sync.Mutex{}
	h.receivedAcks = make(map[string]map[uint64]*Ack)
	h.peerTimeoutsLock = sync.Mutex{}
	h.peerTimeouts = make(map[string]uint64)

	h.ackWaitChan = make(map[uint64]chan bool)
	h.seenPairAcks = make(map[PairAck] bool)
	h.pairAcksLock  = sync.Mutex{}

	for _, peer := range params.PeerHosts {
		h.addPeer(peer)
	}
	h.setUpMessageRPC()
	h.notifyPeers()

	return h
}
