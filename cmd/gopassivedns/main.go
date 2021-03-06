package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/google/gopacket/tcpassembly"
	"github.com/google/gopacket/tcpassembly/tcpreader"
	log "github.com/sirupsen/logrus"
	"github.com/smira/go-statsd"
)

const (
	//  create constant for the packetQueue as this is used in multiple places.
	packetQueue  int    = 500
	zeroInt      int    = 0
	udpString    string = "udp"
	tcpString    string = "tcp"
	packetString string = "packet"
)

var (
	// global channel to recieve reassembled TCP streams
	// consumed in doCapture. THis is global because the interface in gopacket
	// doesn't initiate or have an
	reassemblerChan chan TCPDataStruct
)

// DNSMapEntry for DNS connection table entry
// the 'inserted' value is used in connection table cleanup
type DNSMapEntry struct {
	entry    layers.DNS
	inserted time.Time
}

// connectionTable stores the connection table
type connectionTable struct {
	connections map[string]DNSMapEntry
	sync.RWMutex
}

// TCPDataStruct struct to store reassembled TCP streams
type TCPDataStruct struct {
	DNSData []byte
	IPLayer gopacket.Flow
	Length  int
}

// TCP reassembly stuff, all the work is done in run()
//
type dnsStreamFactory struct{}

type dnsStream struct {
	net, transport gopacket.Flow
	r              tcpreader.ReaderStream
}

func (d *dnsStreamFactory) New(net, transport gopacket.Flow) tcpassembly.Stream {
	dstream := &dnsStream{
		net:       net,
		transport: transport,
		r:         tcpreader.NewReaderStream(),
	}
	go dstream.run() // Important... we must guarantee that data from the reader stream is read.

	// ReaderStream implements tcpassembly.Stream, so we can return a pointer to it.
	return &dstream.r
}

func (d *dnsStream) run() {
	var data []byte
	var tmp = make([]byte, 4096)

	for {
		count, err := d.r.Read(tmp)

		if err == io.EOF {
			//we must read to EOF, so we also use it as a signal to send the reassembed
			//stream into the channel
			// Ensure the length of data is at least two for integer parsing,
			// skip to next iterator if too short
			if len(data) < 2 {
				return
			}
			// Parse the actual integer
			DNSdatalen := int(binary.BigEndian.Uint16(data[:2]))
			// Ensure the length of data is the parsed size +2,
			// skip to next iterator if too short
			if len(data) < DNSdatalen+2 {
				return
			}
			reassemblerChan <- TCPDataStruct{
				DNSData: data[2 : DNSdatalen+2],
				IPLayer: d.net,
				Length:  int(binary.BigEndian.Uint16(data[:2])),
			}
			return
		} else if err != nil {
			log.Debug("Error when reading DNS buf: ", err)
		} else if count > 0 {
			data = append(data, tmp...)
		}
	}
}

//	takes the src IP, dst IP, DNS question, DNS reply and the logs struct to populate.
//	returns nothing, but populates the logs array
func initLogEntry(syslogPriority string, srcIP net.IP, srcPort uint16, dstIP net.IP, length *int, protocol *string, question layers.DNS, answer layers.DNS, timestamp time.Time, logs *[]DNSLogEntry) {

	/*
	   http://forums.devshed.com/dns-36/dns-packet-question-section-1-a-183026.html
	   multiple questions isn't really a thing, so we'll loop over the answers and
	   insert the question section from the original query.  This means a successful
	   ANY query may result in a lot of seperate log entries.  The query ID will be
	   the same on all of those entries, however, so you can rebuild the query that
	   way.

	   TODO: Also loop through Additional records in addition to Answers
	*/

	if *protocol == packetString {
		*protocol = udpString
	}

	var additionals bool
	if len(answer.Additionals) != 0 {
		additionals = true
	}

	// a response code other than 0 means failure of some kind
	if answer.ResponseCode != 0 {
		*logs = append(*logs, DNSLogEntry{
			Level:               syslogPriority,
			QueryID:             answer.ID,
			Question:            string(question.Questions[0].Name),
			ResponseCode:        answer.ResponseCode,
			QuestionType:        TypeString(question.Questions[0].Type),
			Answer:              answer.ResponseCode.String(),
			AnswerType:          "",
			TTL:                 0,
			AuthoritativeAnswer: answer.AA,
			RecursionDesired:    question.RD,
			RecursionAvailable:  question.RA,
			Server:              srcIP, //this is the answer packet, which comes from the server...
			Client:              dstIP, //...and goes to the client
			Timestamp:           time.Now().UTC().String(),
			Elapsed:             time.Now().Sub(timestamp).Nanoseconds(),
			ClientPort:          srcPort,
			Length:              *length,
			Proto:               *protocol,
			Truncated:           answer.TC,
			ResponseSz:          0,
			QuestionSz:          uint16(len(question.Questions[0].Name)),
			Additionals:         additionals,
		})

	} else {
		for _, ans := range answer.Answers {

			*logs = append(*logs, DNSLogEntry{
				QueryID:             answer.ID,
				Question:            string(question.Questions[0].Name),
				ResponseCode:        answer.ResponseCode,
				QuestionType:        TypeString(question.Questions[0].Type),
				Answer:              RRString(ans),
				AnswerType:          TypeString(ans.Type),
				TTL:                 ans.TTL,
				Server:              srcIP, //this is the answer packet, which comes from the server...
				Client:              dstIP, //...and goes to the client
				Timestamp:           time.Now().UTC().String(),
				Elapsed:             time.Now().Sub(timestamp).Nanoseconds(),
				ClientPort:          srcPort,
				Level:               syslogPriority,
				AuthoritativeAnswer: answer.AA, // this is in the header, not the answer slice
				RecursionDesired:    question.RD,
				RecursionAvailable:  question.RA,
				Length:              *length,
				Proto:               *protocol,
				Truncated:           answer.TC,                               // this is in the header, not the answer slice
				ResponseSz:          ans.DataLength,                          // each answer has its own size
				QuestionSz:          uint16(len(question.Questions[0].Name)), // this captures the size of the question name to see name server requet padding in the <payload>.domain.com data exfiltration model.
				Additionals:         additionals,
			})
		}
	}
}

//	background task to clear out stale entries in the conntable
//	takes a pointer to the conntable to clean, the maximum age of an entry and how often to run GC
func cleanDNSCache(conntable *connectionTable, maxAge time.Duration, interval time.Duration, stats *statsd.Client, finished chan bool) {
	scheduled := time.NewTicker(interval)
	for {
		select {
		case <-scheduled.C:
			//max_age should be negative, e.g. -1m
			cleanupCutoff := time.Now().Add(maxAge)
			conntable.RLock()
			for key, item := range conntable.connections {
				if item.inserted.Before(cleanupCutoff) {
					conntable.RUnlock()
					conntable.Lock()
					log.Debug("conntable GC: cleanup query ID " + key)
					delete(conntable.connections, key)
					conntable.Unlock()
					conntable.RLock()
					if stats != nil {
						stats.Incr("cache_entries_dropped", 1)
					}
				}
			}
			conntable.RUnlock()
		case <-finished:
			log.Printf("gopassivedns: cleanDNSCache cleanly exiting %s", time.Now().String())
			return
		}
	}
}

// handleDNS processses the DNS layer
func handleDNS(conntable *connectionTable, dns *layers.DNS, logChan chan DNSLogEntry, syslogPriority string, srcIP, dstIP net.IP, srcPort, dstPort uint16, length *int, protocol *string, packetTime time.Time, stats *statsd.Client) {
	//skip non-query stuff (Updates, AXFRs, etc)
	if dns.OpCode != layers.DNSOpCodeQuery {
		log.Debug("Saw non-query DNS packet")
	}

	//pre-allocated for initLogEntry
	logs := []DNSLogEntry{}

	// generate a more unique key for a conntable map to avoid hash key collisions as dns.ID is not very unique
	var uid string
	if dstPort == 53 {
		uid = fmt.Sprintf("%s->%d:%d", strconv.Itoa(int(dns.ID)), srcPort, dstPort)
	} else {
		uid = fmt.Sprintf("%s->%d:%d", strconv.Itoa(int(dns.ID)), dstPort, srcPort)
	}

	conntable.RLock()
	//lookup the query ID:source port in our connection table
	item, foundItem := conntable.connections[uid]
	//this is a Query Response packet and we saw the question go out...
	//if we saw a leg of this already...
	if foundItem {
		//if we just got the reply
		if dns.QR {
			if stats != nil {
				stats.Incr("log_qr", 1)
			}
			log.Debug("Got 'answer' leg of query ID: " + strconv.Itoa(int(dns.ID)))
			initLogEntry(syslogPriority, srcIP, srcPort, dstIP, length, protocol, item.entry, *dns, item.inserted, &logs)
		} else {
			if stats != nil {
				stats.Incr("log_no_qr", 1)
			}
			//we just got the question, so we should already have the reply. This is most commonly seen with DNS packets over TCP
			log.Debug("Got the 'question' leg of query ID " + strconv.Itoa(int(dns.ID)))
			initLogEntry(syslogPriority, srcIP, srcPort, dstIP, length, protocol, *dns, item.entry, item.inserted, &logs)
		}
		conntable.RUnlock()
		conntable.Lock()
		delete(conntable.connections, uid)
		conntable.Unlock()
		//TODO: send the array itself, not the elements of the array
		//to reduce the number of channel transactions
		for _, logEntry := range logs {
			logChan <- logEntry
		}

	} else {
		//This is the initial query.  save it for later.
		log.Debug("Got a leg of query ID " + strconv.Itoa(int(dns.ID)))
		mapEntry := DNSMapEntry{
			entry:    *dns,
			inserted: packetTime,
		}
		conntable.RUnlock()
		conntable.Lock()
		conntable.connections[uid] = mapEntry
		conntable.Unlock()
	}
}

// validate if DNS packet, make conntable entry and output
//   to log channel if there is a match
//
//   we pass packet by value here because we turned on ZeroCopy for the capture, which reuses the capture buffer
func handlePacket(conntable *connectionTable, packets chan *packetData, logChan chan DNSLogEntry, syslogPriority string, gcInterval time.Duration, gcAge time.Duration, threadNum int, stats *statsd.Client) {
	//TCP reassembly init
	streamFactory := &dnsStreamFactory{}
	streamPool := tcpassembly.NewStreamPool(streamFactory)
	assembler := tcpassembly.NewAssembler(streamPool)
	ticker := time.Tick(time.Minute)

	// do the string conversion once for each goroutine reduces the allocations for each for loop.
	packetWallTimeStatName := strconv.Itoa(threadNum) + ".packet_wall_time"
	dnsLookupsStatName := strconv.Itoa(threadNum) + ".dns_lookups"

	for {
		select {
		case packet, more := <-packets:

			//used for clean shutdowns
			if !more {
				return
			}

			err := packet.Parse()

			if err != nil {
				log.Debugf("Error parsing packet: %s", err)
				continue
			}

			srcIP := packet.GetSrcIP()
			dstIP := packet.GetDstIP()
			srcPort := packet.GetSrcPort()
			dstPort := packet.GetDstPort()

			var packetTime time.Time

			if packet.GetTimestamp() != nil {
				packetTime = *packet.GetTimestamp()
			} else {
				log.Debug("Adding wall time not packet time to message.")
				if stats != nil {
					stats.Incr(packetWallTimeStatName, 1)
				}
				packetTime = time.Now()
			}

			// All TCP goes to reassemble.  This is first because a single packet DNS request will parse as DNS
			// But that will leave the connection hanging around in memory, because the inital handshake won't
			// parse as DNS, nor will the connection closing.

			if packet.IsTCPStream() {
				handleDNS(conntable,
					packet.GetDNSLayer(),
					logChan,
					syslogPriority,
					srcIP,
					dstIP,
					srcPort,
					dstPort,
					packet.GetSize(),
					packet.GetProto(),
					packetTime,
					stats)
			} else if packet.HasTCPLayer() {
				// because most ipv6 packets are dual stack we need to look at the src ip address to identify if its an IPv6 lookup
				// ot ipv4. If we simply look at the layers dual stack includes both.
				if srcIP.To4() != nil {
					assembler.AssembleWithTimestamp(
						packet.GetIPv4Layer().NetworkFlow(),
						packet.GetTCPLayer(), *packet.GetTimestamp())
					continue
				} else {
					assembler.AssembleWithTimestamp(
						packet.GetIPv6Layer().NetworkFlow(),
						packet.GetTCPLayer(), *packet.GetTimestamp())
					continue
				}

			} else if packet.HasDNSLayer() {
				handleDNS(conntable,
					packet.GetDNSLayer(),
					logChan,
					syslogPriority,
					srcIP,
					dstIP,
					srcPort,
					dstPort,
					packet.GetSize(),
					packet.GetProto(),
					packetTime,
					stats)
				if stats != nil {
					stats.Incr(dnsLookupsStatName, 1)
				}
			} else {
				//UDP and doesn't parse as DNS?
				log.Debug("Missing a DNS layer?")
			}
		case <-ticker:
			// Every minute, flush connections that haven't seen activity in the past 2 minutes.
			assembler.FlushOlderThan(time.Now().Add(time.Minute * -2))
		}
	}
}

// setup a device or pcap file for capture, returns a handle
func initHandle(config *pdnsConfig) *pcap.Handle {

	var handle *pcap.Handle
	var err error

	if config.device != "" && !config.pfring {
		handle, err = pcap.OpenLive(config.device, config.snapLen, true, pcap.BlockForever)
		if err != nil {
			log.Debug(err)
			return nil
		}
	} else if config.pcapFile != "" {
		handle, err = pcap.OpenOffline(config.pcapFile)
		if err != nil {
			log.Debug(err)
			return nil
		}
	} else {
		log.Debug("You must specify either a capture device or a pcap file")
		return nil
	}

	err = handle.SetBPFFilter(config.bpf)
	if err != nil {
		log.Debug(err)
		return nil
	}

	return handle
}

// kick off packet procesing threads and start the packet capture loop
func doCapture(handle *pcap.Handle, config *pdnsConfig, logChan chan DNSLogEntry, reassembledChan chan TCPDataStruct, stats *statsd.Client, finished chan bool) {

	gcAgeDur, err := time.ParseDuration(config.gcAge)

	if err != nil {
		log.Fatal("Your gc_age parameter was not parseable.  Use a string like '-1m'")
	}

	gcIntervalDur, err := time.ParseDuration(config.gcInterval)

	if err != nil {
		log.Fatal("Your gc_age parameter was not parseable.  Use a string like '3m'")
	}

	//setup the global channel for reassembled TCP streams
	reassemblerChan = reassembledChan

	/* init channels for the packet handlers and kick off handler threads */
	var channels []chan *packetData
	for i := 0; i < config.numprocs; i++ {
		log.Debugf("Creating packet processing channel %d", i)
		channels = append(channels, make(chan *packetData, packetQueue))
	}

	//DNS IDs are stored as uint16s by the gopacket DNS layer
	var conntable = connectionTable{
		connections: make(map[string]DNSMapEntry),
	}

	//setup garbage collection for this map
	go cleanDNSCache(&conntable, gcAgeDur, gcIntervalDur, stats, finished)

	for i := 0; i < config.numprocs; i++ {
		log.Debugf("Starting packet processing thread %d", i)
		go handlePacket(&conntable, channels[i], logChan, config.syslogPriority, gcIntervalDur, gcAgeDur, i, stats)
	}

	// Use the handle as a packet source to process all packets
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	//only decode packet in response to function calls, this moves the
	//packet processing to the processing threads
	packetSource.DecodeOptions.Lazy = true
	//We don't mutate bytes of the packets, so no need to make a copy
	//this does mean we need to pass the packet via the channel, not a pointer to the packet
	//as the underlying buffer will get re-allocated
	packetSource.DecodeOptions.NoCopy = true

	var ethLayer layers.Ethernet
	var IPv4Layer layers.IPv4
	var IPv6Layer layers.IPv6

	parser := gopacket.NewDecodingLayerParser(
		layers.LayerTypeEthernet,
		&ethLayer,
		&IPv4Layer,
		&IPv6Layer,
	)

	foundLayerTypes := []gopacket.LayerType{}
	scheduled := time.NewTicker(time.Duration(config.statsdInterval) * time.Second)

CAPTURE:
	for {
		select {
		case reassembledTCP := <-reassembledChan:
			pd := newTCPData(reassembledTCP)
			channels[int(reassembledTCP.IPLayer.FastHash())&(config.numprocs-1)] <- pd
			if stats != nil {
				stats.Incr("reassembed_tcp", 1)
			}
		case packet := <-packetSource.Packets():
			if packet != nil {
				parser.DecodeLayers(packet.Data(), &foundLayerTypes)
				if foundLayerType(layers.LayerTypeIPv4, foundLayerTypes) {
					pd := newPacketData(packet)
					channels[int(IPv4Layer.NetworkFlow().FastHash())&(config.numprocs-1)] <- pd
					if stats != nil {
						stats.Incr("packets", 1)
					}
				}
				if foundLayerType(layers.LayerTypeIPv6, foundLayerTypes) {
					pd := newPacketData(packet)
					channels[int(IPv6Layer.NetworkFlow().FastHash())&(config.numprocs-1)] <- pd
					if stats != nil {
						stats.Incr("packets_v6", 1)
					}
				}
			} else {
				//if we get here, we're likely reading a pcap and we've finished
				//or, potentially, the physical device we've been reading from has been
				//downed.  Or something else crazy has gone wrong...so we break
				//out of the capture loop entirely.

				log.Debug("packetSource returned nil")
				break CAPTURE
			}
		case <-scheduled.C:
			handleStats, err := handle.Stats()

			if err != nil {
				log.Printf("gopassivedns: doCapture error getting handle stats %s", err)
				continue
			}

			log.Printf("Statistics received: %d, dropped: %d, interface dropped %d",
				handleStats.PacketsReceived,
				handleStats.PacketsDropped,
				handleStats.PacketsIfDropped,
			)
			if stats != nil {
				stats.Incr("packets_received", int64(handleStats.PacketsReceived))
				stats.Incr("packets_dropped", int64(handleStats.PacketsDropped))
				stats.Incr("packets_ifdropped", int64(handleStats.PacketsIfDropped))
				stats.GetLostPackets()
			}
		case <-finished:
			log.Printf("gopassivedns: doCapture cleanly exiting.")
			break CAPTURE
		}
	}
	gracefulShutdown(channels, reassembledChan, logChan)
}

func main() {

	//insert the ENV as defaults here, then after the parse we add the true defaults if nothing has been set
	//also convert true/false strings to true/false types

	config := initConfig()

	if config.cpuprofile != "" {
		f, err := os.Create(config.cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("Could not start CPU Profile ", err)
		}

		defer f.Close()
		defer pprof.StopCPUProfile()
	}

	var stats *statsd.Client = nil

	if config.statsdHost != "" {
		stats = statsd.NewClient(
			config.statsdHost,
			statsd.TagStyle(statsd.TagFormatDatadog),
			statsd.MetricPrefix(fmt.Sprintf("%s.%s.", config.statsdPrefix, config.sensorName)),
			statsd.FlushInterval(time.Duration(config.statsdInterval)*time.Second),
			statsd.BufPoolCapacity(packetQueue),
			statsd.SendLoopCount(config.numprocs),
		)
	}

	handle := initHandle(config)

	if handle == nil {
		log.Fatal("Could not initilize the capture.")
	}

	logOpts := newLogOptions(config)

	logChan := initLogging(logOpts, config)

	// setup the global reassembledChannel for tcp stream reassembly.
	reassembledChan := make(chan TCPDataStruct)

	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM)

	go watchSignals(sigs, done)

	// spin up logging thread(s)
	go logConn(logChan, logOpts, stats)

	// spin up the actual capture threads
	doCapture(handle, config, logChan, reassembledChan, stats, done)

	log.Debug("Done!  Goodbye.")
}
