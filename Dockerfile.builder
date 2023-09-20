FROM golang:1.21.1-alpine3.18 AS deps

WORKDIR /go/src/github.com/metal-toolbox/conditionorc
COPY go.mod go.sum ./
RUN go mod download
WORKDIR /go
COPY pkg ./pkg

FROM deps AS builder 
WORKDIR /go/src/github.com/metal-toolbox/conditionorc
ARG LDFLAG_LOCATION=github.com/metal-toolbox/conditionorc/internal/version
ARG GIT_COMMIT
ARG GIT_BRANCH
ARG GIT_SUMMARY
ARG VERSION
ARG BUILD_DATE

COPY main.go ./
COPY cmd ./cmd/
COPY internal ./internal 
COPY pkg ./pkg

RUN GOOS=linux GOARCH=amd64 go build -o /bin/conditionorc \
-ldflags \
"-X ${LDFLAG_LOCATION}.GitCommit=${GIT_COMMIT} \
-X ${LDFLAG_LOCATION}.GitBranch=${GIT_BRANCH} \
-X ${LDFLAG_LOCATION}.GitSummary=${GIT_SUMMARY} \
-X ${LDFLAG_LOCATION}.AppVersion=${VERSION} \
-X ${LDFLAG_LOCATION}.BuildDate=${BUILD_DATE}"

FROM alpine:3.18.0
RUN apk -U add curl

WORKDIR /usr/sbin/

COPY --from=builder --chmod=755 /bin/conditionorc .

ENTRYPOINT ["/usr/sbin/conditionorc"]
