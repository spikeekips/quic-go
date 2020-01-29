package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/lucas-clemente/quic-go/http3"
	"github.com/lucas-clemente/quic-go/internal/protocol"
	"github.com/lucas-clemente/quic-go/interop/http09"
	"golang.org/x/sync/errgroup"
)

var errUnsupported = errors.New("unsupported test case")

var tlsConf *tls.Config

func main() {
	logFile, err := os.Create("/logs/log.txt")
	if err != nil {
		fmt.Printf("Could not create log file: %s\n", err.Error())
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	keyLog, err := os.Create("/logs/keylogfile.txt")
	if err != nil {
		fmt.Printf("Could not create key log file: %s\n", err.Error())
		os.Exit(1)
	}
	defer keyLog.Close()

	tlsConf = &tls.Config{
		InsecureSkipVerify: true,
		KeyLogWriter:       keyLog,
	}
	testcase := os.Getenv("TESTCASE")
	if err := runTestcase(testcase); err != nil {
		if err == errUnsupported {
			fmt.Printf("unsupported test case: %s\n", testcase)
			os.Exit(127)
		}
		fmt.Printf("Downloading files failed: %s\n", err.Error())
		os.Exit(1)
	}
}

func runTestcase(testcase string) error {
	flag.Parse()
	urls := flag.Args()

	switch testcase {
	case "http3":
		r := &http3.RoundTripper{TLSClientConfig: tlsConf}
		defer r.Close()
		return downloadFiles(r, urls, false)
	case "handshake", "transfer", "retry":
	case "multiconnect":
		return runMultiConnectTest(urls)
	case "versionnegotiation":
		return runVersionNegotiationTest(urls)
	case "resumption":
		return runResumptionTest(urls, false)
	case "zerortt":
		return runResumptionTest(urls, true)
	default:
		return errUnsupported
	}

	r := &http09.RoundTripper{TLSClientConfig: tlsConf}
	defer r.Close()
	return downloadFiles(r, urls, false)
}

func runVersionNegotiationTest(urls []string) error {
	if len(urls) != 1 {
		return errors.New("expected at least 2 URLs")
	}
	protocol.SupportedVersions = []protocol.VersionNumber{0x1a2a3a4a}
	err := downloadFile(&http09.RoundTripper{}, urls[0], false)
	if err == nil {
		return errors.New("expected version negotiation to fail")
	}
	if !strings.Contains(err.Error(), "No compatible QUIC version found") {
		return fmt.Errorf("expect version negotiation error, got: %s", err.Error())
	}
	return nil
}

func runMultiConnectTest(urls []string) error {
	for _, url := range urls {
		r := &http09.RoundTripper{TLSClientConfig: tlsConf}
		if err := downloadFile(r, url, false); err != nil {
			return err
		}
		if err := r.Close(); err != nil {
			return err
		}
	}
	return nil
}

func runResumptionTest(urls []string, use0RTT bool) error {
	if len(urls) < 2 {
		return errors.New("expected at least 2 URLs")
	}

	tlsConf.ClientSessionCache = tls.NewLRUClientSessionCache(1)

	// do the first transfer
	r := &http09.RoundTripper{TLSClientConfig: tlsConf}
	if err := downloadFiles(r, urls[:1], false); err != nil {
		return err
	}
	r.Close()

	// reestablish the connection, using the session ticket that the server (hopefully provided)
	r = &http09.RoundTripper{TLSClientConfig: tlsConf}
	defer r.Close()
	return downloadFiles(r, urls[1:], use0RTT)
}

func downloadFiles(cl http.RoundTripper, urls []string, use0RTT bool) error {
	var g errgroup.Group
	for _, u := range urls {
		url := u
		g.Go(func() error {
			return downloadFile(cl, url, use0RTT)
		})
	}
	return g.Wait()
}

func downloadFile(cl http.RoundTripper, url string, use0RTT bool) error {
	method := http.MethodGet
	if use0RTT {
		method = http09.MethodGet0RTT
	}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}
	rsp, err := cl.RoundTrip(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()

	file, err := os.Create("/downloads" + req.URL.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, rsp.Body)
	return err
}
