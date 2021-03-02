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
	"errors"
	"fmt"
	"sync"
	"time"

	pb "github.com/qyzhaoxun/sriov-cni/pkg/nodeservice"

	apierror "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	corev1 "k8s.io/api/core/v1"

	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"k8s.io/klog/v2"
)

const (
	UnderlayIP          = "tke.networking.cloud.com/rdma-vtep-remote-ip"
	PodCIDR             = "tke.networking.cloud.com/rdma-overlay-pod-cidr"
	controllerAgentName = "rdma-overlay-agent"
)

type NodeOverlayInfo struct {
	PodCIDR string
	UnderlayIP string
}

// Controller is the controller implementation for Foo resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface

	nodesLister corelisters.NodeLister
	nodesSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder

	nodeCache map[string]*NodeOverlayInfo
	nLock      *sync.RWMutex

	grpcClient pb.NodeServiceClient

	context context.Context
}

// NewController returns a new sample controller
func NewController(
	ctxt context.Context,
	kubeclientset kubernetes.Interface,
	nodeInformer coreinformers.NodeInformer,
	gRPCClient pb.NodeServiceClient) *Controller {

	// Create event broadcaster
	klog.V(4).Info("creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		context:       ctxt,
		grpcClient:    gRPCClient,
		kubeclientset: kubeclientset,
		nodesLister:   nodeInformer.Lister(),
		nodesSynced:   nodeInformer.Informer().HasSynced,
		// TODO rate limit: 指数回退 or 线性回退 ？ 回退最大值？
		workqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerAgentName),
		recorder:      recorder,
		nodeCache:      make(map[string]*NodeOverlayInfo),
		nLock:      new(sync.RWMutex),
	}

	klog.Info("setting up event handlers")

	nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueNode,
		UpdateFunc: func(old, new interface{}) {
			newNode := new.(*corev1.Node)
			oldNode := old.(*corev1.Node)
			newNodeInfo, _ := GetOverlayInfo(newNode)
			oldNodeInfo, _ := GetOverlayInfo(oldNode)
			if oldNodeInfo == nil && newNodeInfo != nil {
				controller.enqueueNode(new)
			}
		},
		DeleteFunc: controller.enqueueNode,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("starting cluster rdma overlay controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.nodesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("starting workers")
	// Launch two workers to process Foo resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("started workers")
	<-stopCh
	klog.Info("shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the Node resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Foo resource
// with the current status of the resource.

// syncItem will sync the node with the given key if it has had
// its expectations fulfilled, meaning it did not expect to see any more of its
// namespaces created or deleted. This function is not meant to be invoked
// concurrently with the same key.
func (c *Controller) syncHandler(key string) error {
	_, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}
	// node, err := c.nodesLister.Get(name)
	_, err = c.nodesLister.Get(name)

	switch {
	case apierror.IsNotFound(err):
		klog.Info(fmt.Sprintf("node %s has been deleted, attempting to cleanup resources", key))
		err = c.handleNodeRemove(key)
	case err != nil:
		klog.Warning(fmt.Sprintf("unable to retrieve Node %s from store, err %+v", key, err))
	default:
		//klog.Info(fmt.Sprintf("Start syncing Node %s", node))
		err = c.handleNodeAddOrUpdate(key)
	}
	return err
}

// enqueueNode takes a Node resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Foo.
func (c *Controller) enqueueNode(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

func (c *Controller) handleNodeAddOrUpdate(key string) error {
	klog.Infof("handle node %s add event", key)

	node, err := c.nodesLister.Get(key)
	if err != nil {
		return err
	}

	// TODO if node == current node

	// if node annotation does not have RDMA overlay info, it is not RDMA overlay node yet, just return
	// RDMA node would receive update event, call it here
	nodeInfo, err := GetOverlayInfo(node)
	if err != nil {
		klog.Infof("could not add node %s: %v", key, err)
		return nil
	}

	c.AddNodeInfoToCache(node.Name, nodeInfo)

	nodeRequest := &pb.NodeReq{
		UnderlayIp: nodeInfo.UnderlayIP,
		PodCIDR:    nodeInfo.PodCIDR,
	}

	// TODO cache add another member
	// TODO what if add failed
	r, err := c.grpcClient.AddClusterNode(c.context, nodeRequest)
	if err != nil {
		klog.Errorf("could not add node: %v", err)
		return err
	}
	klog.Infof("add node %s success: %d", node.Name, r.Ret)

	return nil
}

func (c *Controller) handleNodeRemove(key string) error {
	klog.Infof("handle node %s remove event", key)

	// if not rdma overlay node, just return
	nodeInfo, err := c.GetNodeInfoFromCache(key)
	if err != nil {
		klog.Infof("could not remove node %s: %v", key, err)
		return nil
	}

	nodeRequest := &pb.NodeReq{
		UnderlayIp: nodeInfo.UnderlayIP,
		PodCIDR:    nodeInfo.PodCIDR,
	}

	r, err := c.grpcClient.RemoveClusterNode(c.context, nodeRequest)
	if err != nil {
		klog.Errorf("could not remove node: %v", err)
		return err
	}
	klog.Infof("remove node success: %d", r.Ret)

	c.DeleteNodeInfoFromCache(key)

	return nil
}

func GetOverlayInfo(node *corev1.Node) (nodeInfo *NodeOverlayInfo, err error) {
	var ok bool
	underlayIP, ok := node.Annotations[UnderlayIP]
	if !ok {
		return nil, errors.New("could not find underlay ip in node annotation")
	}
	podCIDR, ok := node.Annotations[PodCIDR]
	if !ok {
		return nil, errors.New("could not find pod cidr in node annotation")
	}
	nodeInfo = &NodeOverlayInfo{podCIDR, underlayIP}
	return nodeInfo, nil
}


func (c *Controller) AddNodeInfoToCache(key string, nodeInfo *NodeOverlayInfo) {
	c.nLock.Lock()
	defer c.nLock.Unlock()

	_, ok := c.nodeCache[key]
	if !ok {
		klog.Infof("add node %s to node cache", key)
		c.nodeCache[key] = nodeInfo
	}
}

func (c *Controller) DeleteNodeInfoFromCache(key string) {
	c.nLock.Lock()
	defer c.nLock.Unlock()

	klog.Infof("delete node %s from node cache", key)
	delete(c.nodeCache, key)
}

func (c *Controller) GetNodeInfoFromCache(key string) (nodeInfo *NodeOverlayInfo, err error) {
	c.nLock.RLock()
	defer c.nLock.RUnlock()

	nodeInfo, ok := c.nodeCache[key]
	if !ok {
		err = errors.New("could not get node overlay info from cache cache")
		return nil, err
	}
	return nodeInfo, nil
}
