// Command document-service-healthcheck is the container probe for
// document-service. It exists as a separate binary so the runtime image needs no
// shell and the probe holds no database credentials.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	address := flag.String("address", "127.0.0.1:18091", "probe base address")
	timeout := flag.Duration("timeout", 2*time.Second, "probe timeout")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}
	response, err := client.Get(fmt.Sprintf("http://%s/readyz", *address))
	if err != nil {
		fmt.Fprintln(os.Stderr, "document-service-healthcheck:", err)
		os.Exit(1)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "document-service-healthcheck: unexpected status %d\n", response.StatusCode)
		os.Exit(1)
	}
}
