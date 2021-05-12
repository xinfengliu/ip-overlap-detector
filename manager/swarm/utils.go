package swarm

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/docker/swarmkit/api"
	"github.com/docker/swarmkit/xnet"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	swarmSocket = "/var/run/docker/swarm/control.sock"
)

func GetSwarmNodes() ([]*api.Node, error) {
	logrus.Debug("Getting swarm nodes info")

	conn, err := getSwarmConn()
	if err != nil {
		return nil, fmt.Errorf("error getting swarm connection: %v", err)
	}
	defer conn.Close()

	c := api.NewControlClient(conn)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFunc()
	r, err := c.ListNodes(ctx,
		&api.ListNodesRequest{},
		grpc.MaxCallRecvMsgSize(math.MaxInt32))
	if err != nil {
		return nil, err
	}

	return r.Nodes, nil
}

func GetSwarmServices() ([]*api.Service, error) {
	logrus.Debug("Getting swarm services")

	conn, err := getSwarmConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	c := api.NewControlClient(conn)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFunc()
	r, err := c.ListServices(ctx,
		&api.ListServicesRequest{},
		grpc.MaxCallRecvMsgSize(math.MaxInt32))
	if err != nil {
		return nil, fmt.Errorf("error listServices(): %v", err)
	}
	return r.Services, nil
}

func GetSwarmNetworks() ([]*api.Network, error) {
	logrus.Debug("Getting swarm networks")

	conn, err := getSwarmConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	c := api.NewControlClient(conn)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFunc()
	r, err := c.ListNetworks(ctx,
		&api.ListNetworksRequest{},
		grpc.MaxCallRecvMsgSize(math.MaxInt32))
	if err != nil {
		return nil, fmt.Errorf("error ListNetworks(): %v", err)
	}
	return r.Networks, nil
}

func getSwarmConn() (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{}
	insecureCreds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	opts = append(opts, grpc.WithTransportCredentials(insecureCreds))
	opts = append(opts, grpc.WithDialer(
		func(addr string, timeout time.Duration) (net.Conn, error) {
			return xnet.DialTimeoutLocal(addr, timeout)
		}))
	conn, err := grpc.Dial(swarmSocket, opts...)
	if err != nil {
		return nil, fmt.Errorf("error getting swarm connection: %v", err)
	}
	return conn, nil
}
