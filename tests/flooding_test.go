package tests
//
//import (
//	"../node"
//	"testing"
//)
//
///*
//This test no longer passes because we are choosing hosts based on latency.
//*/
//func TestFlooding(t *testing.T) {
//	clientIp := "127.0.0.1"
//
//	//host.Initialize("../parameters/parameters1.json") //Vancouver
//	//host.Initialize("../parameters/parameters2.json") //Berlin
//	//host.Initialize("../parameters/parameters3.json") //London
//	//host.Initialize("../parameters/parameters4.json") //Beijing
//	host5 := node.Initialize("../parameters/parameters5.json") //Osaka
//	//host.Initialize("../parameters/parameters6.json") //Seattle
//
//	expectedHost := "127.0.0.1:5000"
//	foundHost := host5.FindHostForClient(clientIp + ":5050")
//	if foundHost != expectedHost {
//		t.Errorf("Expected %s, got %s", expectedHost, foundHost)
//	}
//	//
//	//expectedHost = "127.0.0.1:9000"
//	//foundHost = host3.FindHostForClient(clientIp + ":5052")
//	//if foundHost != expectedHost {
//	//	t.Errorf("Expected %s, got %s", expectedHost, foundHost)
//	//}
//	//
//	//expectedHost = "127.0.0.1:4000"
//	//foundHost = host5.FindHostForClient(clientIp + ":5051")
//	//if foundHost != expectedHost {
//	//	t.Errorf("Expected %s, got %s", expectedHost, foundHost)
//	//}
//}
