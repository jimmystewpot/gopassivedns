package main

import (
	"bufio"
	"fmt"

	"log/syslog"
	"net"

	"strconv"
	"strings"
	"time"

	"github.com/google/gopacket/layers"
	"github.com/pquerna/ffjson/ffjson"
	log "github.com/sirupsen/logrus"
	"github.com/smira/go-statsd"
	"github.com/vmihailenco/msgpack"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// codebeat:disable[TOO_MANY_IVARS]
type logOptions struct {
	quiet          bool
	debug          bool
	Filename       string
	FluentdSocket  string
	MaxAge         int
	MaxBackups     int
	MaxSize        int
	KafkaBrokers   string
	KafkaTopic     string
	SyslogFacility string
	SyslogPriority string
	SensorName     string
	closed         bool
	control        chan string
}

// newLogOptions returns the logging configuration
func newLogOptions(config *pdnsConfig) *logOptions {
	return &logOptions{
		quiet:          config.quiet,
		debug:          config.debug,
		Filename:       config.logFile,
		FluentdSocket:  config.fluentdSocket,
		KafkaBrokers:   config.kafkaBrokers,
		KafkaTopic:     config.kafkaTopic,
		MaxAge:         config.logMaxAge,
		MaxSize:        config.logMaxSize,
		MaxBackups:     config.logMaxBackups,
		SyslogFacility: config.syslogFacility,
		SyslogPriority: config.syslogPriority,
		SensorName:     config.sensorName,
	}
}

func (lo *logOptions) IsDebug() bool {
	return lo.debug
}

func (lo *logOptions) LogToStdout() bool {
	return !lo.quiet
}

func (lo *logOptions) LogToFile() bool {
	return !(lo.Filename == "")
}

func (lo *logOptions) LogToKafka() bool {
	return !(lo.KafkaBrokers == "" && lo.KafkaTopic == "")
}

func (lo *logOptions) LogToSyslog() bool {
	return (lo.SyslogFacility != "" && lo.SyslogPriority != "")
}

func (lo *logOptions) LogToFluentd() bool {
	return (lo.FluentdSocket != "")
}

// DNSLogEntry is the JSON mapping of field names to the struct for logging output.
// codebeat:disable[TOO_MANY_IVARS]
type DNSLogEntry struct {
	QueryID             uint16                 `json:"query_id"`
	ResponseCode        layers.DNSResponseCode `json:"rcode"`
	Question            string                 `json:"q"`
	QuestionType        string                 `json:"qtype"`
	Answer              string                 `json:"a"`
	AnswerType          string                 `json:"atype"`
	TTL                 uint32                 `json:"ttl"`
	Server              net.IP                 `json:"dst"`
	Client              net.IP                 `json:"src"`
	Timestamp           string                 `json:"tstamp"`
	Elapsed             int64                  `json:"elapsed"`
	ClientPort          uint16                 `json:"sport"`
	Level               string                 `json:"level"` // syslog level
	Length              int                    `json:"bytes"` // kept for legacy reasons
	Proto               string                 `json:"protocol"`
	Truncated           bool                   `json:"truncated"`
	AuthoritativeAnswer bool                   `json:"aa"`
	RecursionDesired    bool                   `json:"rd"`
	RecursionAvailable  bool                   `json:"ra"`
	ResponseSz          uint16                 `json:"response_size"` // response size
	QuestionSz          uint16                 `json:"question_size"` // question size
	Additionals         bool                   `json:"additionals"`
	encoded             []byte                 //to hold the marshaled data structure
	err                 error                  //encoding errors
}

// codebeat:enable[TOO_MANY_IVARS]

//private, idempotent function that ensures the json is encoded
func (dle *DNSLogEntry) ensureEncoded() {
	if dle.encoded == nil && dle.err == nil {
		dle.encoded, dle.err = ffjson.Marshal(dle)
	}
}

// Size returns the size of the encoded entry.
func (dle *DNSLogEntry) Size() int {
	dle.ensureEncoded()
	return len(dle.encoded)
}

// Encode the returned log entry
func (dle *DNSLogEntry) Encode() ([]byte, error) {
	dle.ensureEncoded()
	return dle.encoded, dle.err
}

func initLogging(opts *logOptions, config *pdnsConfig) chan DNSLogEntry {
	if opts.IsDebug() {
		log.SetLevel(log.DebugLevel)
	}

	//TODO: further logging setup?

	/* spin up logging channel */
	var logChan = make(chan DNSLogEntry, packetQueue*config.numprocs)

	return logChan

}

func watchLogStats(stats *statsd.Client, logC chan DNSLogEntry, logs []chan DNSLogEntry) {
	for {
		stats.Gauge("incoming_log_depth", int64(len(logC)))
		for i, logChan := range logs {
			stats.Gauge(strconv.Itoa(i)+".log_depth", int64(len(logChan)))
		}

		time.Sleep(15 * time.Second)
	}
}

//Spin up required logging threads and then round-robin log messages to log sinks
func logConn(logC chan DNSLogEntry, opts *logOptions, stats *statsd.Client) {

	//holds the channels for the outgoing log channels
	var logs []chan DNSLogEntry

	if opts.LogToStdout() {
		log.Debug("STDOUT logging enabled")
		stdoutChan := make(chan DNSLogEntry)
		logs = append(logs, stdoutChan)
		go logConnStdout(stdoutChan)
	}

	if opts.LogToFile() {
		log.Debug("file logging enabled to " + opts.Filename)
		fileChan := make(chan DNSLogEntry)
		logs = append(logs, fileChan)
		go logConnFile(fileChan, opts)
	}

	if opts.LogToKafka() {
		log.Debug("kafka logging enabled")
		kafkaChan := make(chan DNSLogEntry)
		logs = append(logs, kafkaChan)
		go logConnKafka(kafkaChan, opts)
	}

	if opts.LogToSyslog() {
		log.Debug("syslog logging enabled")
		syslogChan := make(chan DNSLogEntry)
		logs = append(logs, syslogChan)
		go logConnSyslog(syslogChan, opts)
	}

	if opts.LogToFluentd() {
		log.Debug("fluentd logging enabled")
		fluentdlogChan := make(chan DNSLogEntry)
		logs = append(logs, fluentdlogChan)
		go logConnFluentd(fluentdlogChan, opts)
	}

	if stats != nil {
		go watchLogStats(stats, logC, logs)
	}

	//setup is done, now we sit here and dispatch messages to the configured sinks
	for message := range logC {
		for _, logChan := range logs {
			logChan <- message
		}
	}

	//if the range exits, the channel was closed, so close the other channels
	for _, logChan := range logs {
		close(logChan)
	}

	return
}

//logs to stdout
func logConnStdout(logC chan DNSLogEntry) {
	for message := range logC {
		encoded, _ := message.Encode()
		fmt.Println(string(encoded))
	}
}

//logs to a file
func logConnFile(logC chan DNSLogEntry, opts *logOptions) {

	logger := &lumberjack.Logger{
		Filename:   opts.Filename,
		MaxSize:    opts.MaxSize, // megabytes
		MaxBackups: opts.MaxBackups,
		MaxAge:     opts.MaxAge, //days
	}

	enc := ffjson.NewEncoder(bufio.NewWriter(logger))

	for message := range logC {
		enc.Encode(message)
	}

	logger.Close()

}

//logs to kafka
func logConnKafka(logC chan DNSLogEntry, opts *logOptions) {
	for message := range logC {
		encoded, _ := message.Encode()
		fmt.Println("Kafka: " + string(encoded))

	}
}

//logs to syslog
func logConnSyslog(logC chan DNSLogEntry, opts *logOptions) {

	level, err := levelToType(opts.SyslogPriority)
	if err != nil {
		log.Fatalf("string '%s' did not parse as a priority", opts.SyslogPriority)
	}
	facility, err := facilityToType(opts.SyslogFacility)
	if err != nil {
		log.Fatalf("string '%s' did not parse as a facility", opts.SyslogFacility)
	}

	logger, err := syslog.New(facility|level, "")
	if err != nil {
		log.Fatalf("failed to connect to the local syslog daemon: %s", err)
	}

	for message := range logC {
		encoded, _ := message.Encode()
		logger.Write([]byte(encoded))
	}
}

//logs to fluentd via a unix socket
func logConnFluentd(logC chan DNSLogEntry, opts *logOptions) {
	Tag := opts.SensorName + ".service"
	tag, _ := msgpack.Marshal(Tag)

	conn := fluentdSocket(opts.FluentdSocket)
	defer conn.Close()

	for message := range logC {
		tm, _ := msgpack.Marshal(time.Now().Unix())
		rec, err := msgpack.Marshal(&message)

		if err != nil {
			fmt.Println(err)
		}

		encoded := []byte{0x93}
		encoded = append(encoded, tag...)
		encoded = append(encoded, tm...)
		encoded = append(encoded, rec...)

		_, err = conn.Write(encoded)

		if err != nil {
			log.Fatalf("Unable to write to UNIX Socket %+v with err %+v\n", opts.FluentdSocket, err)
		}
	}
}

func facilityToType(facility string) (syslog.Priority, error) {
	facility = strings.ToUpper(facility)
	switch facility {
	case "KERN":
		return syslog.LOG_KERN, nil
	case "USER":
		return syslog.LOG_USER, nil
	case "MAIL":
		return syslog.LOG_MAIL, nil
	case "DAEMON":
		return syslog.LOG_DAEMON, nil
	case "AUTH":
		return syslog.LOG_AUTH, nil
	case "SYSLOG":
		return syslog.LOG_SYSLOG, nil
	case "LPR":
		return syslog.LOG_LPR, nil
	case "NEWS":
		return syslog.LOG_NEWS, nil
	case "UUCP":
		return syslog.LOG_UUCP, nil
	case "CRON":
		return syslog.LOG_CRON, nil
	case "AUTHPRIV":
		return syslog.LOG_AUTHPRIV, nil
	case "FTP":
		return syslog.LOG_FTP, nil
	case "LOCAL0":
		return syslog.LOG_LOCAL0, nil
	case "LOCAL1":
		return syslog.LOG_LOCAL1, nil
	case "LOCAL2":
		return syslog.LOG_LOCAL2, nil
	case "LOCAL3":
		return syslog.LOG_LOCAL3, nil
	case "LOCAL4":
		return syslog.LOG_LOCAL4, nil
	case "LOCAL5":
		return syslog.LOG_LOCAL5, nil
	case "LOCAL6":
		return syslog.LOG_LOCAL6, nil
	case "LOCAL7":
		return syslog.LOG_LOCAL7, nil
	default:
		return 0, fmt.Errorf("invalid syslog facility: %s", facility)
	}
}

func levelToType(level string) (syslog.Priority, error) {
	level = strings.ToUpper(level)
	switch level {
	case "EMERG":
		return syslog.LOG_EMERG, nil
	case "ALERT":
		return syslog.LOG_ALERT, nil
	case "CRIT":
		return syslog.LOG_CRIT, nil
	case "ERR":
		return syslog.LOG_ERR, nil
	case "WARNING":
		return syslog.LOG_WARNING, nil
	case "NOTICE":
		return syslog.LOG_NOTICE, nil
	case "INFO":
		return syslog.LOG_INFO, nil
	case "DEBUG":
		return syslog.LOG_DEBUG, nil
	default:
		return 0, fmt.Errorf("Unknown priority: %s", level)
	}
}

func fluentdSocket(path string) *net.UnixConn {
	var retries int = 10
	var timeout time.Duration = 5

	// we want to have retries because fluentd can take some time to start.
	for i := 1; i <= retries; i++ {
		raddr, err := net.ResolveUnixAddr("unix", path)

		if err != nil {
			log.Printf("Failed to open remote socket. %s.\n", err)
		}

		conn, err := net.DialUnix("unix", nil, raddr)

		if err != nil {
			log.Printf("Failed to connect to fluentd socket. %s retrying in 5 seconds.", err)
			time.Sleep(timeout * time.Second)
			continue
		}

		err = conn.SetWriteBuffer(65536)

		if err != nil {
			log.Printf("Unable to set fluentd write buffer. %s", err)
		}

		return conn
	}

	log.Fatalf("Unable to open connection to fluentd socket after %d retries\n", retries)

	return nil
}
