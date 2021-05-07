package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	"github.com/xinfengliu/ip-overlap-detector/api"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type server struct{}

const (
	port = ":50051"
)

var verbose bool

func init() {
	flag.BoolVar(&verbose, "D", false, "enable debugging log")
	flag.Parse()
	if verbose {
		logrus.SetLevel(logrus.DebugLevel)
	}
}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	lis, err := net.Listen("tcp", port)
	if err != nil {
		logrus.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	api.RegisterWorkerServer(s, &server{})

	go func() {
		sig := <-sigs
		logrus.Infof("Received the signal '%v'.", sig)
		logrus.Infof("Stopping gRPC server...")
		s.Stop()
		fmt.Println("Finished.")
		os.Exit(0)
	}()

	logrus.Info("Starting gRPC server...")
	if err := s.Serve(lis); err != nil {
		logrus.Fatalf("failed to serve: %v", err)
	}
}

func (s *server) GetNetContainerInfo(context.Context, *emptypb.Empty) (*api.GetNetContainerInfoResponse, error) {
	results, err := inspectNetworks()
	if err != nil {
		logrus.Error("Error inspectNetworks() =>", err)
		return nil, err
	}
	return &api.GetNetContainerInfoResponse{Results: results}, nil
}

func inspectNetworks() ([]*api.NetContainerInfo, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("error getting docker client: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	filter := types.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("scope", "swarm"), filters.Arg("driver", "overlay")),
	}
	nets, err := cli.NetworkList(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("error NetworkList(): %v", err)
	}

	results := []*api.NetContainerInfo{}
	for _, n := range nets {
		if n.Name == "ingress" {
			continue
		}
		logrus.Debugf("Network: %s", n.Name)

		nr, err := cli.NetworkInspect(ctx, n.ID, types.NetworkInspectOptions{})
		if err != nil {
			if client.IsErrNotFound(err) {
				logrus.Warnf("The network '%s' might have been just deleted.", n.Name)
				continue
			} else {
				return nil, fmt.Errorf("error NetworkInspect() on network '%s' : %v", n.Name, err)
			}
		}
		nci := api.NetContainerInfo{Net: n.Name}
		if nr.Containers != nil && len(nr.Containers) > 0 {
			for _, e := range nr.Containers {
				//if e.Name == fmt.Sprintf("%s-endpoint", n.Name) {
				logrus.Debugf("    Container: %s, IP: %s", e.Name, e.IPv4Address)
				nci.Containers = append(nci.Containers, &api.ContainerInfo{
					Name: e.Name,
					Ip:   e.IPv4Address,
				})
				//break
				//}
			}
		}
		results = append(results, &nci)
	}
	return results, nil
}
