package checker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/swarmkit/api"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	napi "github.com/xinfengliu/ip-overlap-detector/api"
	"github.com/xinfengliu/ip-overlap-detector/manager/swarm"
)

type Opts struct {
	WorkerRPCPort int
	MaxWorkers    int
}

type job struct {
	nodeName string
	nodeIP   string
}

type nodeNetInfo struct {
	nodeName         string
	netContainerInfo []*napi.NetContainerInfo
}

// This is a distributed application processing. To make it under
// control, be sure to setup deadlines for each step processing.
func Run(opts *Opts) {
	logrus.Info("Run docker overlay network IP overlap checking...")
	// find swarm nodes and node network attachments via swarmkit API
	nodes, err := swarm.GetSwarmNodes()
	if err != nil {
		logrus.Error("Error getSwarmNodes():", err)
		logrus.Fatal("Quiting.")
	}
	nodeNAMap := getNodeNAMap(nodes)
	svcVIPMap := getServiceVIPMap()

	// For this application, it's enough to use simple concurrency method from
	// https://gobyexample.com/worker-pools
	// But need to be careful in using channels, worker must not abort for each processing.
	// A general and robust approach would be using 'pipeline' pattern like
	// https://blog.golang.org/pipelines
	nNodes := len(nodes)
	jobC := make(chan job, nNodes)
	resultC := make(chan nodeNetInfo, nNodes)

	for _, node := range nodes {
		nodeName := node.Description.Hostname
		if node.Status.State != api.NodeStatus_READY {
			logrus.Warnf("Swarm node '%s' status is not ready, status: %s", nodeName, node.Status.State)
		}

		nodeIP := node.Status.Addr
		//https://github.com/moby/moby/issues/35437#issuecomment-592492655
		if node.Status.Addr == "0.0.0.0" && node.Role == api.NodeRoleManager {
			nodeIP = strings.SplitN(node.ManagerStatus.Addr, ":", 2)[0]
			logrus.Warnf("Swarm node '%s' addr is 0.0.0.0, use ManagerStatus.Addr instead: '%s'", nodeName, nodeIP)
		}
		jobC <- job{nodeName, nodeIP}
	}

	//  gRPC calls to all nodes to collect node network info
	nWorker := nNodes
	if nNodes > opts.MaxWorkers {
		nWorker = opts.MaxWorkers
	}
	for w := 1; w <= nWorker; w++ {
		go worker(jobC, resultC, opts.WorkerRPCPort)
	}
	close(jobC)

	// Wait for all workers done and transform node network info to map
	// Each worker must send result to resultC even if there are errors in doing work.
	nodeNetInfoMap := make(map[string]map[string][]*napi.ContainerInfo, nNodes)
	for a := 1; a <= nNodes; a++ {
		nodeNetInfo := <-resultC
		netInfoMap := make(map[string][]*napi.ContainerInfo)
		for _, info := range nodeNetInfo.netContainerInfo {
			netInfoMap[info.Net] = info.Containers
		}
		nodeNetInfoMap[nodeNetInfo.nodeName] = netInfoMap
	}

	// Info collection is done at this point. Run checks.
	check(nodeNetInfoMap, nodeNAMap, svcVIPMap)
}

func getNodeNAMap(nodes []*api.Node) map[string]map[string]string {
	nodeNAMap := make(map[string]map[string]string, len(nodes))
	for _, node := range nodes {
		nodeName := node.Description.Hostname
		logrus.Debugf("Swarm Node: %s", nodeName)
		logrus.Debug("  NetworkAttachments:")
		naMap := make(map[string]string)
		for _, na := range node.Attachments {
			netName := na.Network.Spec.Annotations.Name
			if netName == "ingress" {
				continue
			}
			var netLBIP string
			if na.Addresses != nil && len(na.Addresses) == 1 {
				netLBIP = na.Addresses[0]
			} else if len(na.Addresses) > 1 {
				logrus.Errorf("Multiple IP addresses for Network Attachment, network: %s, IP: %v", netName, na.Addresses)
			}
			logrus.Debugf("    Network: %s, IP: %s", netName, netLBIP)
			naMap[netName] = netLBIP
		}
		nodeNAMap[nodeName] = naMap
	}
	return nodeNAMap
}

type serviceDetails struct {
	service string
	net     string
	vip     string
}

func (s serviceDetails) String() string {
	return fmt.Sprintf("{service='%s', net='%s', vip='%s'}", s.service, s.net, s.vip)
}

type containerDetails struct {
	node string
	net  string
	name string
	ip   string
}

func (c containerDetails) String() string {
	return fmt.Sprintf("{node='%s', net='%s', container='%s', ip='%s'}", c.node, c.net, c.name, c.ip)
}

func getServiceVIPMap() (svcVIPMap map[string][]*serviceDetails) {
	svcVIPMap = map[string][]*serviceDetails{}
	svcs, err := swarm.GetSwarmServices()
	if err != nil {
		logrus.Error("Error GetSwarmServices():", err)
		return
	}
	nets, err := swarm.GetSwarmNetworks()
	if err != nil {
		logrus.Error("Error GetSwarmNetworks():", err)
		return
	}
	netMap := map[string]string{}
	for _, net := range nets {
		netMap[net.ID] = net.Spec.Annotations.Name
	}

	for _, svc := range svcs {
		vips := svc.Endpoint.VirtualIPs
		for _, vip := range vips {
			netName := netMap[vip.NetworkID]
			if netName == "ingress" {
				continue
			}
			v := serviceDetails{
				service: svc.Spec.Annotations.Name,
				net:     netName,
				vip:     vip.Addr,
			}

			svcVIPMap[vip.Addr] = append(svcVIPMap[vip.Addr], &v)
			logrus.Debugf("Service VIP Info => %v", v)
		}
	}
	return
}

func check(nodeNetInfoMap map[string]map[string][]*napi.ContainerInfo,
	nodeNAMap map[string]map[string]string, svcVIPMap map[string][]*serviceDetails) {

	logrus.Debug("Begin: IP check.")

	ipToContainerMap := make(map[string][]*containerDetails)
	var lbErrCnt, olErrCnt uint
	for node, netInfoMap := range nodeNetInfoMap {
		naMap := nodeNAMap[node]
		// when netInfoMap is nil, e.g. getNodeNetinfo() returns err or the node does not run any
		// containers with overlay network, below codes are skipped automtatically.
		for net, containers := range netInfoMap {
			// on swarm manager nodes, 'docker network' API lists all networks even if there's no containers running on the network on the node.
			if len(containers) == 0 {
				continue
			}
			naIp := naMap[net]
			for _, c := range containers {
				if c.Name == fmt.Sprintf("%s-endpoint", net) {
					logrus.Debugf("Libnetwork=> Node: %s, Net: %s, LB IP: %s", node, net, c.Ip)
					if c.Ip != naIp {
						if naIp != "" {
							logrus.Errorf("Incorrect Node LB IP. node: %s, net: %s, LB IP: %s, NetworkAttachment IP: %s", node, net, c.Ip, naIp)
						} else {
							// there are containers on the net on the node from libnetwork's view, but there's no
							// network attachment from swarm's view. It may be transient (the containers haven't been cleaned up yet)
							logrus.Errorf("Incorrect Node LB IP. node: %s, net: %s, LB IP: %s, but the NetworkAttachment not existing in swarm.", node, net, c.Ip)
						}
						lbErrCnt++
					}
				} else {
					logrus.Debugf("Libnetwork=> Node: %s, Net: %s, Container: %s, IP: %s", node, net, c.Name, c.Ip)
				}
				ipToContainerMap[c.Ip] = append(ipToContainerMap[c.Ip], &containerDetails{node, net, c.Name, c.Ip})
			}
		}
	}

	for ip, cs := range ipToContainerMap {
		if len(cs) > 1 {
			logrus.Errorf("Found IP overlap=> IP: %s, %v", ip, cs)
			olErrCnt++
		} else if v, ok := svcVIPMap[ip]; ok {
			logrus.Errorf("Found IP overlap with service VIP => IP: %s, containers: %v, service VIP: %v", ip, cs, v)
			olErrCnt++
		} else {
			logrus.Debugf("OK=> IP: %s, %v", ip, cs)
		}
	}

	if lbErrCnt == 0 {
		logrus.Info("Node LB IP check finished, no errors found.")
	} else {
		logrus.Infof("Node LB IP check finished, found %d errors", lbErrCnt)
	}

	if olErrCnt == 0 {
		logrus.Info("IP overlap check finished, no errors found.")
	} else {
		logrus.Infof("IP overlap check finished, found %d errors", olErrCnt)
	}
	logrus.Debug("End: IP check.")
}

// retrieve job, make gRPC call to a node to collect node network info
func worker(jobC <-chan job, results chan<- nodeNetInfo, port int) {
	for job := range jobC {
		addr := fmt.Sprintf("%s:%d", job.nodeIP, port)
		nodeName := job.nodeName
		r, err := getNodeNetinfo(addr, nodeName)
		if err != nil {
			logrus.Errorf("Error in getNodeNetinfo for node '%s'. %v", nodeName, err)
		}
		results <- nodeNetInfo{nodeName, r}
	}
}

func getNodeNetinfo(address, nodeName string) ([]*napi.NetContainerInfo, error) {
	logrus.Debugf("Getting network info of containers for node '%s', address: %s", nodeName, address)

	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("grpc Dial addr: %s failed, reason: %v", address, err)
	}
	defer conn.Close()
	c := napi.NewWorkerClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	r, err := c.GetNetContainerInfo(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("grpc GetNetContainerInfo() failed, reason: %v", err)
	}

	results := r.GetResults()
	// if b, err := json.Marshal(results); err != nil {
	// 	logrus.Warn(err)
	// } else {
	// 	logrus.Debugf("getNodeNetinfo=> node: %s, result: %s", nodeName, string(b))
	// }

	return results, nil
}
