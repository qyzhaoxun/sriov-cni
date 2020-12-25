#!/bin/sh

cp -f /rdma /host-cni-bin/

cp -f /sriov /host-cni-bin/

if [ ! -e /host-cni-etc/20-tke-sriov-rdma.conflist ]; then
	cp /20-tke-sriov-rdma.conflist /host-cni-etc/
fi

while sleep 3600; do :; done