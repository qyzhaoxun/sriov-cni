FROM golang:1.14-alpine as builder

ARG GIT_COMMIT=$(git rev-list -1 HEAD)

ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

# config
WORKDIR /go/src/nodeWatcher
COPY go.mod .
COPY go.sum .
RUN GO111MODULE=on go mod download
COPY . .
RUN go install -ldflags "-s -w -X main.GitCommitId=$GIT_COMMIT" ./cmd/node-watcher/
RUN go install -ldflags "-s -w -X main.GitCommitId=$GIT_COMMIT" ./cmd/fake-grpc-server/
RUN go install -ldflags "-s -w -X main.GitCommitId=$GIT_COMMIT" ./cmd/sriov/

FROM mellanox/rdma-cni as RDMA-CNI

# runtime image
FROM gcr.io/google_containers/ubuntu-slim:0.14
COPY --from=RDMA-CNI /usr/bin/rdma /bin/rdma
COPY --from=builder /go/bin/node-watcher /bin/node-watcher
COPY --from=builder /go/bin/fake-grpc-server /bin/fake-grpc-server
COPY --from=builder /go/bin/sriov /bin/sriov
COPY entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]