package host

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

//Public IP, private IP and location of the host
var publicIp string
var privateIp string
var location Location

var clientPort string  //Port that clients will connect to
var hostPortRPC string //Port that hosts will connect using RPC
var hostPortUDP string //Port that hosts will send upd messages

//Peer ip addresses
var peers = make(map[string]bool)
var peerLock = sync.Mutex{}

//Client address. An empty string if there is no client
var client string
var clientLock = sync.Mutex{}

//Seen host requests (prevent endless looping during flooding)
var seenHostRequests = make(map[HostRequest]bool)
var seenHostRequestsLock = sync.Mutex{}

type Parameters struct {
	HostLatitude      float64  `json:"HostLatitude"`
	HostLongitude     float64  `json:"HostLongitude"`
	HostID            string   `json:"HostID"`
	PeerHosts         []string `json:"PeerHosts"`
	HostPublicIP      string   `json:"HostPublicIP"`
	HostPrivateIP     string   `json:"HostPrivateIP"`
	AcceptClientsPort string   `json:"AcceptClientsPort"`
	HostsPortRPC      string   `json:"HostsPortRPC"`
	HostsPortUDP      string   `json:"HostsPortUDP"`
}

type Message struct{}

//Add peer to the peer list
func (t *Message) RpcAddPeer(ip string, reply *int) error {
	addPeer(ip)
	return nil
}

func (t *Message) ReceiveHostRequest(args HostRequestWithSender, reply *int) error {
	hostRequest := args.Request

	seenHostRequestsLock.Lock()
	_, ok := seenHostRequests[hostRequest]
	seenHostRequests[hostRequest] = true
	seenHostRequestsLock.Unlock()

	if !ok {
		clientLock.Lock()
		available := client == ""
		clientLock.Unlock()

		sendResponse := func() {
			hostRequest.BestHost = concatIp(publicIp, clientPort)
			hostRequest.BestHostLocation = location
			conn, err := net.Dial("udp", hostRequest.RequestingHost)
			if err == nil {
				b, err := marshallHostRequest(hostRequest)
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
				sendResponse()
			} else {
				currentBestDistance := distance(hostRequest.ClientLocation, hostRequest.BestHostLocation)
				hostDistance := distance(hostRequest.ClientLocation, location)
				if hostDistance < currentBestDistance {
					sendResponse()
				}
			}
		}
		floodHostRequest(args.Sender, hostRequest)
	}
	return nil
}

func Initialize(paramsPath string) {
	var params Parameters
	jsonFile, err := os.Open(paramsPath)
	if err != nil {
		log.Println(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	json.Unmarshal(byteValue, &params)
	log.Println(params)

	publicIp = params.HostPublicIP
	privateIp = params.HostPrivateIP
	location = Location{Latitude: params.HostLatitude, Longitude: params.HostLongitude}
	hostPortRPC = params.HostsPortRPC
	hostPortUDP = params.HostsPortUDP
	clientPort = params.AcceptClientsPort

	for _, peer := range params.PeerHosts {
		addPeer(peer)
	}
	setUpMessageRPC()
	notifyPeers()
}

//Handle RPC messages
func setUpMessageRPC() {
	msg := new(Message)
	rpc.Register(msg)
	rpc.HandleHTTP()
	l, e := net.Listen("tcp", concatIp(privateIp, hostPortRPC))
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//Notify peers that this host has joined
func notifyPeers() {
	peerLock.Lock()
	defer peerLock.Unlock()
	for peer := range peers {
		var reply int
		client, err := rpc.DialHTTP("tcp", peer)
		if err != nil {
			log.Println(err)
		}
		client.Go("Message.RpcAddPeer", concatIp(publicIp, hostPortRPC), reply, nil)
	}
}

//Add a peer to the peers list
func addPeer(ip string) {
	peerLock.Lock()
	defer peerLock.Unlock()
	peers[ip] = true
}

//Send the host request to all peers
func floodHostRequest(sender string, hostRequest HostRequest) {
	peerLock.Lock()
	defer peerLock.Unlock()
	for peer := range peers {
		if peer != sender {
			var reply int
			hostRequestWithSender := HostRequestWithSender{
				Sender: concatIp(publicIp, hostPortRPC),
				Request: hostRequest}
			client, err := rpc.DialHTTP("tcp", peer)
			if err != nil {
				log.Println(err)
			}
			client.Go("Message.ReceiveHostRequest", hostRequestWithSender, reply, nil)
		}
	}
}

//Will either return the ip of the best host, or an empty string if there are no hosts
func FindHostForClient(clientLocation Location) string {
	hostRequest := HostRequest{
		ClientLocation: clientLocation,
		RequestingHost: concatIp(publicIp, hostPortUDP)}
	floodHostRequest(concatIp(publicIp, hostPortRPC), hostRequest)
	return waitForBestHost(concatIp(privateIp, hostPortUDP), clientLocation)
}

//Wait for hosts to respond. Then choose the best host
func waitForBestHost(addr string, clientLocation Location) string {
	bestHost := ""
	bestHostLocation := Location{}
	currentBestDistance := float64(0)

	//If this host has no clients, add the host as the best host
	clientLock.Lock()
	if client == "" {
		bestHost = concatIp(publicIp, clientPort)
		bestHostLocation = location
		currentBestDistance = distance(clientLocation, location)
	}
	clientLock.Unlock()

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

	//Only wait for one second
	timeoutTime := time.Now().Add(time.Second * 3)
	l.SetReadDeadline(timeoutTime)

	for time.Now().Before(timeoutTime) {
		buffer := make([]byte, 1024)
		n, _, err := l.ReadFromUDP(buffer)
		if err == nil {
			hostRequest, err := unMarshallHostRequest(buffer, n)
			if err == nil {
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
			}
		}
	}
	log.Println(bestHostLocation)
	return bestHost
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
