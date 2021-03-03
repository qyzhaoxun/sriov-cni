/*
 * Copyright 2019 THL A29 Limited, a Tencent company.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"time"

	pb "github.com/qyzhaoxun/sriov-cni/pkg/nodeservice"
	"github.com/qyzhaoxun/sriov-cni/pkg/signals"

	"google.golang.org/grpc"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

var (
	gRPCURL         string
	rdmaOverlayCidr string
)

func initKubeClient() *kubernetes.Clientset {
	kubeconfigFile := os.Getenv("KUBECONFIG")
	var err error
	var config *rest.Config

	if _, err = os.Stat(kubeconfigFile); err != nil {
		klog.Infof("kubeconfig %s failed to find due to %v", kubeconfigFile, err)
		config, err = rest.InClusterConfig()
		if err != nil {
			klog.Fatalf("failed building incluster kubeconfig %v", err)
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigFile)
		if err != nil {
			klog.Fatalf("error building kubeconfig: %s", err.Error())
		}
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("error building kubernetes clientset: %s", err.Error())
	}
	return client
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func ComputeRange(cidr string) (rangeStart, rangeEnd string, err error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		klog.Errorf("parse cidr failed: %v ", err)
		return "", "", err
	}

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}
	// remove network address and broadcast address
	return ips[2], ips[len(ips)-2], nil
}

func ConfigCNI(overlayIP, podCIDR string) (err error) {
	rangeStart, rangeEnd, err := ComputeRange(podCIDR)
	if err != nil {
		return err
	}
	GenSRIOVConf("eth2", rdmaOverlayCidr, rangeStart, rangeEnd, DefaultCNIConfDir)

	return nil
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	kubeClient := initKubeClient()

	klog.Info("grpc url ", gRPCURL)

	// Set up a connection to the server.
	conn, err := grpc.Dial(gRPCURL, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	grpcClient := pb.NewNodeServiceClient(conn)

	// Contact the server and print out its response.
	ctxt, cancel := context.WithCancel(context.Background())
	defer cancel()

	// gRPC call init node
	curNodeName := os.Getenv("NODE_NAME")
	curNode, err := kubeClient.CoreV1().Nodes().Get(ctxt, curNodeName, metav1.GetOptions{})
	if err != nil {
		klog.Fatalf("could not get current node: %v", err)
	}

	// TODO if have not annotation
	// 指数退避尝试 Get Overlay Info
	nodeInfo, err := GetOverlayInfo(curNode)
	if err != nil {
		klog.Fatalf("could not get node info: %v", err)
	}

	nodeRequest := &pb.NodeReq{
		UnderlayIp: nodeInfo.UnderlayIP,
		PodCIDR:    nodeInfo.PodCIDR,
	}

	_, err = grpcClient.InitNode(ctxt, nodeRequest)
	if err != nil {
		log.Fatalf("could not init node: %v", err)
	}
	klog.Infof("init node %s success: underlay ip: %s, pod cidr: %s", curNode.Name, nodeInfo.UnderlayIP, nodeInfo.PodCIDR)

	rdmaOverlayCidr = os.Getenv("RDMA_OVERLAY_CIDR")
	ConfigCNI(nodeInfo.UnderlayIP, nodeInfo.PodCIDR)

	// create and start controller
	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
	nodeInformer := kubeInformerFactory.Core().V1().Nodes()
	controller := NewController(ctxt, kubeClient, nodeInformer, grpcClient)

	// notice that there is no need to run Start methods in a separate goroutine. (i.e. go kubeInformerFactory.Start(stopCh)
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	kubeInformerFactory.Start(stopCh)

	if err := controller.Run(2, stopCh); err != nil {
		klog.Fatalf("error running controller: %s", err.Error())
	}
}

func init() {
	flag.StringVar(&gRPCURL, "remote-overlay-service", "", "The address of the gRPC server which serves as remote overlay service.")
}