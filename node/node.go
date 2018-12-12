package node

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"github.com/DistributedClocks/GoVector/govec"
	"github.com/DistributedClocks/GoVector/govec/vrpc"
	"github.com/sparrc/go-ping"
	"io/ioutil"
	"log"
	"net"
	"net/rpc"
	"os"
	"strings"
	"sync"
	"time"
)

const MaxTimeouts = 3
const DEBUG_CONSENSUS_ACCEPTER = true
const DEBUG_CONSENSUS_PROPOSER = true

type Node struct {
	nodeID string

	// public ip of node
	PublicIp string

	//For other hosts to connect using RPC
	privateAddrRPC string
	publicAddrRPC  string

	//For other hosts to connect using UDP
	privateAddrUDP string
	publicAddrUDP  string

	//Used to connect to client
	publicAddrClient  string

	//Used to receive consensus connection
	publicVerificationPortIp string
	privateVerificationPortIp string
	privateVerificationReturnPortIp string
	blackList []string

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

	//Host stream for sending and receiving data to/from client
	HostStream *HostStream

	//Client stream for sending and receiving data to/from host
	ClientStream *ClientStream
}

type NodeInterface interface {
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
	FindHostForClient(clientAddr string)
	monitorNode(peer string)
	waitForBestHost(addr string, clientAddr string, seqNum uint64) (map[string] string, string, string)
	ListenForHostErrors()
	ListenForClientErrors(clientAddr string)
}

var(
	GovecOptions = govec.GetDefaultLogOptions()
)

//Receive a heart beat from another host
func (h *Node) ReceiveHeartBeat(hb *HeartBeat, reply *int) error {
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
func (h *Node) ReceiveACK(ack *Ack, reply *int) error {
	//log.Println(h.publicAddrRPC + " Received ACK from " + ack.Sender + " Seq num: " + strconv.Itoa(int(ack.HBeatSeqNum)))

	h.failureDetectorLock.Lock()
	defer h.failureDetectorLock.Unlock()
	h.receivedAcks[ack.Sender][ack.HBeatSeqNum] = ack
	return nil
}


//Add peer to this host's peer list
func (h *Node) RpcAddPeer(ip string, reply *int) error {
	h.addPeer(ip)
	return nil
}

//Receive an acknowledgement for a client-host pairing
func (h *Node) ReceivePairAck(pairAck PairAck, reply *int) error {
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
func (h *Node) ReceivePair(pair HostClientPair, reply *int) error {
	h.seenPairingsLock.Lock()
	_, ok := h.seenPairings[pair]
	h.seenPairings[pair] = true
	h.seenPairingsLock.Unlock()
	fmt.Println("received client request at: " + h.nodeID)

	//If we haven't seen the message before, check if we are the host that is being paired
	if !ok {
		if pair.Host == h.publicAddrClient {
			h.clientLock.Lock()
			var pairAck PairAck
			fmt.Println("host being paired: " + h.nodeID)


			//Only accept if we are currently available
			if h.clientAddr == "" && h.clientConnected == false {
				fmt.Println("received client request")
				h.clientAddr = pair.Client
				h.clientConnected = true

				// Begin streaming and accepting client input
				go h.ListenForHostErrors()
				h.HostStream.ConnectToClient(pair.Client, pair.Host)

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
		} else {
			//Flood pairing to peers
			h.floodHostClientPair(pair)
		}
	}
	return nil
}

func (h *Node) ListenForHostErrors() {
	select {
	case err := <-h.HostStream.hostErrorReceived:
		fmt.Println(err)

		h.clientLock.Lock()
		h.clientAddr = ""
		h.clientConnected = false
		h.clientLock.Unlock()
		// TODO: reset host state back to available
	}
}

func (h *Node) ReceiveHostRequest(args HostRequestWithSender, reply *int) error {
	hostRequest := args.Request

	h.seenHostRequestsLock.Lock()
	_, ok := h.seenHostRequests[hostRequest]
	h.seenHostRequests[hostRequest] = true
	h.seenHostRequestsLock.Unlock()

	if !ok {
		h.clientLock.Lock()
		available := !h.clientConnected
		h.clientLock.Unlock()

		sendResponse := func(avgRtt time.Duration, respondingHostAddr string) {
			messageToSend := HostResponse{
				SequenceNumber: hostRequest.SequenceNumber,
				RequestingHost: hostRequest.RequestingHost,
				RespondingHostAddr: respondingHostAddr,
				RespondingHost: h.nodeID,
				AvgRTT: avgRtt,
				SenderVerificationLAddr: h.publicVerificationPortIp,
			}
			conn, err := net.Dial("udp", hostRequest.RequestingHost)
			if err == nil {
				b, err := marshallHostResponse(messageToSend)
				if err == nil {
					conn.Write(b)
				}
			} else {
				log.Println(err)
			}
		}

		//If this host is available and is better than the host in the current request, send itself to the requesting host
		if available {
			split := strings.Split(hostRequest.ClientAddr, ":")
			pinger, err := ping.NewPinger(split[0])
			if err == nil {
				pinger.Count = 3
				pinger.Run()
				stats := pinger.Statistics()
				sendResponse(stats.AvgRtt, h.publicAddrClient)
			} else {
				sendResponse(time.Duration(-1), "")
				log.Println(err)
			}
		} else {
			sendResponse(time.Duration(-1), "")
		}
		h.floodHostRequest(args.Sender, hostRequest)
	}
	return nil
}

//Handle RPC messages
func (h *Node) setUpMessageRPC() {
	handler := rpc.NewServer()
	handler.Register(h)

	l, e := net.Listen("tcp", h.privateAddrRPC)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go vrpc.ServeRPCConn(handler, l, h.govecLogger, GovecOptions)
}

//Notify peers that this host has joined
func (h *Node) notifyPeers() {
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
func (h *Node) addPeer(ip string) {
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
func (h *Node) floodPairAck(pairAck PairAck) {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()

	for _, client := range h.peers {
		var reply int
		client.Go("Host.ReceivePairAck", pairAck, reply, nil)
	}
}

//Send the host request to all peers
func (h *Node) floodHostRequest(sender string, hostRequest HostRequest) {
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
func (h *Node) floodHostClientPair(pair HostClientPair) {
	h.peerLock.Lock()
	defer h.peerLock.Unlock()

	for _, client := range h.peers {
		var reply int
		client.Go("Host.ReceivePair", pair, reply, nil)
	}
}


//After consensus is done for the client-host pairing, call this function to send the pairing to all hosts
func (h *Node) sendHostClientPair(clientAddr string, hostAddr string) bool {
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

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
//CONSENSES PROPOSE METHODS

// Ask hosts that responded to the flooding for agreement to accept client
func (h *Node) askForAgreement(hostId string,respondedHosts map[string]string) bool{
	conn,err := getConnection(h.privateVerificationReturnPortIp)
	if err != nil {
                log.Fatal(err)
        }
	defer conn.Close()
	decisionChan := make(chan bool)
	go waitForDecisions(respondedHosts, conn, decisionChan)

	for _, v := range respondedHosts {
		vm := VerificationMesssage{HostId: hostId, ReturnIp: conn.LocalAddr().String()}
		h.proposeAcceptence(v, vm)
	}
	return <-decisionChan
}

// Sends a single propose message to host
func (h *Node) proposeAcceptence(remoteIpPort string, vm VerificationMesssage){
        conn, err := net.Dial("udp", remoteIpPort)
        if err != nil {
                log.Printf("Cannot resolve UDPAddress: Error %s\n", err)
                return
        }
        var network bytes.Buffer
        enc := gob.NewEncoder(&network)
        err = enc.Encode(vm)
        if err != nil {
                log.Printf("Cannot encode to VerificationMesssage: Error %s\n", err)
                return
        }
        _, err = conn.Write(network.Bytes())
        if err != nil {
                log.Println("Error writing to UDP", err)
        }
}

// waits and concludes final decision wether to accept client.
func waitForDecisions(respondedHosts map[string]string, conn *net.UDPConn, notifyChan chan bool){
	defer conn.Close()
	var network bytes.Buffer
	buf := make([]byte, 1024)
	respondedHostsDecisions := make(map[string]bool)
	conn.SetReadDeadline(time.Now().Add(1*time.Second))
	for end := time.Now().Add(3 * time.Second); ; {
		if time.Now().After(end) {
			break
		}
		var dm DecisionMessage
                n, _, err := conn.ReadFromUDP(buf)
                if err != nil {
			//log.Println(err)
			continue
                }
                network.Write(buf[0:n])
                dec := gob.NewDecoder(&network)
                err = dec.Decode(&dm)
                if err != nil {
                        log.Printf("Cannot decode to uint32: Error %s\n", err)
			continue
                }
		if _, ok := respondedHosts[dm.HostId]; ok {
			if(DEBUG_CONSENSUS_PROPOSER){
				log.Println("adding decision for host:", dm.HostId)
			}
			respondedHostsDecisions[dm.HostId] = dm.Decision
		}
                network.Reset()
	}

	totalReqs := len(respondedHosts)
	totalAgrees := 0
	for _, v := range respondedHostsDecisions {
		if(v){
			totalAgrees++
		}
        }

	if(DEBUG_CONSENSUS_PROPOSER){
		log.Println("Total Proposes:", totalReqs)
		log.Println("Total Agrees:", totalAgrees)
	}
	if(totalAgrees - (totalReqs/2) >= 0){
		notifyChan <- true
	} else{
		notifyChan <- false
	}
}
//CONSENSES PROPOSE METHODS END
////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func getConnection(ip string) (conn *net.UDPConn, err error) {
	lAddr, err := net.ResolveUDPAddr("udp", ip)
	if err != nil {
		return nil, err
	}
	l, err := net.ListenUDP("udp", lAddr)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return l, nil
}

//TODO: this method probably doesn't need to be capitalized. Only is capitalize right now for testing purposes
func (h *Node) FindHostForClient(clientAddr string) {
	fmt.Println("finding new host")
	h.seqNumberLockHR.Lock()
	seqNum := h.currSeqNumberHR
	h.currSeqNumberHR++
	h.seqNumberLockHR.Unlock()

	//Create the host request and mark it as seen
	hostRequest := HostRequest{
		SequenceNumber: seqNum,
		ClientAddr: clientAddr,
		RequestingHost: h.publicAddrUDP}
	h.seenHostRequestsLock.Lock()
	h.seenHostRequests[hostRequest] = true
	h.seenHostRequestsLock.Unlock()

	//Flood to network then wait for responses
	h.floodHostRequest(h.publicAddrRPC, hostRequest)

	respondedHosts, bestHost, bestHostId := h.waitForBestHost(h.privateAddrUDP, clientAddr, seqNum)

	if respondedHosts == nil {
		log.Println("No hosts are currently available.")
		return
	}

	log.Println(respondedHosts) //Array of host IDs that responded to the flooded request

	decision := h.askForAgreement(bestHostId, respondedHosts)
	if(DEBUG_CONSENSUS_PROPOSER){
		log.Println("HostId:", bestHostId ,"Network Decision:", decision)
	}

	if !decision {
		return
	}

	//Flood the best client-host pairing
	accept := h.sendHostClientPair(clientAddr, bestHost)
	if accept {
		fmt.Println("Host " + bestHost + " has agreed to accept client " + clientAddr)

		// make ClientStream begin sending/receiving from best host
		h.ClientStream.ConnectToHost(clientAddr, bestHost)
		h.ListenForClientErrors(clientAddr)
	} else {
		log.Println("Host " + bestHost + " won't accept client " + clientAddr)
	}
}

func (h *Node) ListenForClientErrors(clientAddr string) {
	select {
	case err := <-h.ClientStream.clientErrorReceived:
		fmt.Println(err)
		time.Sleep(10 * time.Second)
		h.FindHostForClient(clientAddr)
		// TODO: reset client state back to available
	}
}

//Wait for hosts to respond. Then choose the best host
func (h *Node) waitForBestHost(addr string, clientAddr string, seqNum uint64) (map[string]string, string, string) {
	bestHostAddr := ""
	bestHostId   := ""
	bestHostTime := time.Duration(1*time.Minute)

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

	//Only wait for five seconds
	timeoutTime := time.Now().Add(time.Second * 5)
	l.SetReadDeadline(timeoutTime)

	respondedHosts := make(map[string]string)

	for time.Now().Before(timeoutTime) {
		buffer := make([]byte, 1024)
		n, _, err := l.ReadFromUDP(buffer)
		if err == nil {
			hostResponse, err := unMarshallHostResponse(buffer, n)
			log.Println(hostResponse)
			h.govecLogger.LogLocalEvent(": Location info from:" + hostResponse.RequestingHost, GovecOptions)
			if err == nil && hostResponse.SequenceNumber == seqNum {
				//If there is currently no best host, set the received host as the best host
				if bestHostAddr == "" && hostResponse.RespondingHostAddr != "" {
					bestHostAddr = hostResponse.RespondingHostAddr
					bestHostId = hostResponse.RespondingHost
					bestHostTime = hostResponse.AvgRTT
				} else if bestHostAddr != "" && hostResponse.RespondingHostAddr != "" {
					baseline := time.Now()
					currentBest := baseline.Add(bestHostTime)
					newHostRTT := baseline.Add(hostResponse.AvgRTT)
					if newHostRTT.Before(currentBest) {
						bestHostAddr = hostResponse.RespondingHostAddr
						bestHostId = hostResponse.RespondingHost
						bestHostTime = hostResponse.AvgRTT
					}
				}
				respondedHosts[hostResponse.RespondingHost] = hostResponse.SenderVerificationLAddr
			}
		}
	}

	if bestHostAddr == "" {
		return nil, "", ""
	} else {
		return respondedHosts, bestHostAddr, bestHostId
	}
}

//Monitor a host node
func (h *Node) monitorNode(peer string) {
	//log.Println("Begin monitoring: " + peer)

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
	log.Println(h.nodeID + "- peer has failed: " + peer)
	delete(h.peers, peer)
	h.peerLock.Unlock()
}


func concatIp(ip string, port string) string {
	return ip + ":" + port
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////
//CONSENSUS ACCEPTER METHODS
func (h *Node) handleVerificationRequests(){
	conn, err := getConnection(h.privateVerificationPortIp)
	if err != nil {
                log.Fatal(err)
        }

	var network bytes.Buffer
	buf := make([]byte, 1024)
	for {
		var vm VerificationMesssage
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		//transfer from byte array to buffer because the canonical
		//io.Copy(&network,conn) requires size checks.
		network.Write(buf[0:n])
		dec := gob.NewDecoder(&network)
		err = dec.Decode(&vm)
		//fmt.Println(hbm)
		if err != nil {
			log.Printf("Cannot decode to uint32: Error %s\n", err)
		}
		if(DEBUG_CONSENSUS_ACCEPTER){
			log.Println("incoming verification request: ",vm)
		}
		h.respondDecision(vm)
		network.Reset()
	}
}

func (h *Node) respondDecision(vm VerificationMesssage){
	// TODO filter with blacklist
	dm := DecisionMessage{HostId: h.nodeID, Decision:true}
	for _,v := range h.blackList {
		if(v == vm.HostId){
			dm.Decision = false
		}
	}
	conn, err := net.Dial("udp", vm.ReturnIp)
        if err != nil {
                log.Printf("Cannot resolve UDPAddress: Error %s\n", err)
                return
        }
        var network bytes.Buffer
        enc := gob.NewEncoder(&network)
        err = enc.Encode(dm)
        if err != nil {
                log.Printf("Cannot encode to VerificationMesssage: Error %s\n", err)
                return
        }
        _, err = conn.Write(network.Bytes())
        if err != nil {
                log.Println("Error writing to UDP", err)
        }
}
//////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func Initialize(paramsPath string) (*Node) {
	var params Parameters
	jsonFile, err := os.Open(paramsPath)
	if err != nil {
		log.Println(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	json.Unmarshal(byteValue, &params)
	log.Println(params)

	h := &Node{}
	h.nodeID = params.NodeID
	h.blackList = params.BlackList

	h.privateAddrRPC = concatIp(params.HostPrivateIP, params.HostsPortRPC)
	h.publicAddrRPC = concatIp(params.HostPublicIP, params.HostsPortRPC)
	h.privateAddrUDP = concatIp(params.HostPrivateIP, params.HostsPortUDP)
	h.publicAddrUDP = concatIp(params.HostPublicIP, params.HostsPortUDP)
	h.publicAddrClient = concatIp(params.HostPublicIP, params.AcceptClientsPort)

	h.PublicIp = params.HostPublicIP

	h.privateVerificationPortIp = concatIp(params.HostPrivateIP, params.VerificationPortUDP)
	h.publicVerificationPortIp = concatIp(params.HostPublicIP, params.VerificationPortUDP)
	h.privateVerificationReturnPortIp = concatIp(params.HostPrivateIP, params.VerificationReturnPortUDP)

	h.govecLogger = govec.InitGoVector(params.NodeID, "./logs/" + params.NodeID, govec.GetDefaultConfig())

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

	// initialize the host streaming service
	h.HostStream = &HostStream{}

	//initialize the client streaming service
	h.ClientStream = &ClientStream{}

	h.clientConnected = !params.Available

	for _, peer := range params.PeerHosts {
		h.addPeer(peer)
	}
	h.setUpMessageRPC()
	h.notifyPeers()
	go h.handleVerificationRequests()

	return h
}
