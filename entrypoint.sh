#!/usr/bin/env sh

echo "=====Starting installing SRIOV-RMDA-CNI ========="

cp /bin/rdma /host-cni-bin/
cp /bin/sriov /host-cni-bin/

echo "=====Starting tke-sriov-rdma-agent ==========="
/bin/node-watcher $@