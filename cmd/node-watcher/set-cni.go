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
    "fmt"
    "io/ioutil"
    "os"
    "path"

    "k8s.io/klog/v2"
)

const (
    pluginName        = "tke-sriov-rdma"
    DefaultCNIConfDir = "/host-cni-etc"
    ClusterSubnet     = "10.0.0.0/16"
)

const (
    routeConfListTemplate = `{
    "cniVersion": "0.3.1",
    "name": "tke-sriov-rdma",
    "plugins": [%s
    ]
}`

    SRIOVConfTemplate = `
        {
            "type": "sriov",
            "if0": "%s",
            "ipam": {
                "type": "host-local",
                "subnet": "%s",
                "rangeStart": "%s",
                "rangeEnd": "%s"
            }
        }`

    RDMAConf = `
        {
            "type": "rdma",
            "args": {
                "cni": {
                  "debug": true
                }
            }
        }`
)

func GenSRIOVConf(netif, subnet, rangeStart, rangeEnd, confDir string) error {
    sriovConf := fmt.Sprintf(SRIOVConfTemplate, netif, subnet, rangeStart, rangeEnd)
    fileName := fmt.Sprintf("20-%s.conflist", pluginName)

    pluginConf := sriovConf + "," + RDMAConf

    cniConfList := fmt.Sprintf(routeConfListTemplate, pluginConf)
    klog.Infof("generate %s conf %s : %s", pluginName, fileName, cniConfList)

    if _, err := os.Stat(confDir); os.IsNotExist(err) {
        if err1 := os.Mkdir(confDir, 0755); err1 != nil {
            klog.Errorf("make conf dir %s failed, err: %v", confDir, err1)
            return err1
        }
    }

    klog.Infof("create conf file %s", fileName)
    return ioutil.WriteFile(path.Join(confDir, fileName), []byte(cniConfList), 0644)
}
