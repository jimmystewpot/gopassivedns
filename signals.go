package main

import (
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// If we shut down without doing this stuff, we will lose some of the packet data
// still in the processing pipeline.
func gracefulShutdown(channels []chan *packetData, reassembledChan chan TCPDataStruct, logChan chan DNSLogEntry) {

	var waitTime int = 6
	var numprocs int = len(channels)

	log.Debug("Draining TCP data...")

OUTER:
	for {
		select {
		case reassembledTCP := <-reassembledChan:
			pd := newTCPData(reassembledTCP)
			channels[int(reassembledTCP.IPLayer.FastHash())&(numprocs-1)] <- pd
		case <-time.After(6 * time.Second):
			break OUTER
		}
	}

	log.Debug("Stopping packet processing...")
	for i := 0; i < numprocs; i++ {
		close(channels[i])
	}

	log.Debug("waiting for log pipeline to flush...")
	close(logChan)

	for len(logChan) > 0 {
		waitTime--
		if waitTime == 0 {
			log.Debug("exited with messages remaining in log queue!")
			return
		}
		time.Sleep(time.Second)
	}
}

// handle a graceful exit so that we do not lose data when we restart the service.
func watchSignals(sig chan os.Signal, done chan bool) {
	for {
		select {
		case <-sig:
			log.Println("Caught signal about to cleanly exit.")
			done <- true
			// Sleeping 15 seconds while the gracefulshutdown function completes.
			time.Sleep(15 * time.Second)
			return
		}
	}
}
