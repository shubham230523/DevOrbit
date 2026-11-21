package main

import (
	"log"

	"devorbit.net/services/connection-broker/internal/broker"
)

func main() {
	b := broker.NewBroker()
	log.Printf("Connection Broker initialized with %d sessions", b.ActiveSessionCount())
}
