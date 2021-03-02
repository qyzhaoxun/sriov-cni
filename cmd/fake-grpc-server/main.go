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
	"log"
	"net"

	pb "github.com/qyzhaoxun/sriov-cni/pkg/nodeservice"
	"google.golang.org/grpc"

	"k8s.io/klog/v2"
)

const (
	port = ":50057"
)

// server is used to implement helloworld.GreeterServer.
type server struct {
	pb.UnimplementedNodeServiceServer
}

func (s *server) InitNode(ctx context.Context, nodeReq *pb.NodeReq) (*pb.NodeRes, error) {
	klog.Infof("Call Init Node %v",  nodeReq)
	return &pb.NodeRes{Ret: 0, ErrMsg: ""}, nil
}

func (s *server) AddClusterNode(ctx context.Context, nodeReq *pb.NodeReq) (*pb.NodeRes, error) {
	klog.Infof("Call Add Cluster Node %v", nodeReq)
	return &pb.NodeRes{Ret: 0, ErrMsg: ""}, nil
}

func (s *server) RemoveClusterNode(ctx context.Context, nodeReq *pb.NodeReq) (*pb.NodeRes, error) {
	klog.Infof("Call Remove Cluster Node %v",  nodeReq)
	return &pb.NodeRes{Ret: 0, ErrMsg: ""}, nil
}

func main() {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterNodeServiceServer(s, &server{})
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}