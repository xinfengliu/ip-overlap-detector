package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xinfengliu/ip-overlap-detector/manager/checker"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	workerRPCPort int
	intervalSec   int
	maxWorkers    int
	verbose       bool
	addr          string
)

func init() {
	flag.IntVar(&workerRPCPort, "worker_port", 50051, "Worker server published port.")
	flag.IntVar(&intervalSec, "interval", 600, "interval (seconds) for running the check.")
	flag.IntVar(&maxWorkers, "c", 30, "max concurrency in getting net info from all nodes.")
	flag.BoolVar(&verbose, "D", false, "enable debugging log")
	flag.StringVar(&addr, "l", ":9111", "The address to listen on for HTTP requests.")
	flag.Parse()
	if verbose {
		logrus.SetLevel(logrus.DebugLevel)
	}
}

func main() {
	logrus.Infof("Start the manager service. The IP overlap checking interval is %d seconds", intervalSec)
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		logrus.Infof("Received the signal '%v'.", sig)
		fmt.Println("Finished.")
		os.Exit(0)
	}()

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		logrus.Fatal(http.ListenAndServe(addr, nil))
	}()

	opts := checker.Opts{
		WorkerRPCPort: workerRPCPort,
		MaxWorkers:    maxWorkers,
	}

	// do once first right after the service starting.
	logrus.Info("Run IP overlap checking for the first time after startup...")
	logrus.Info("It's possible that the worker service has not been ready yet, errors may happen for this first run.")
	checker.Run(&opts)

	for t := range ticker.C {
		logrus.Debug(t)
		checker.Run(&opts)
	}
}
