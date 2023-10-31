FROM golang:1.21.3-alpine3.18 AS build

WORKDIR /go/src/github.com/metal-toolbox/conditionorc
COPY go.mod go.sum ./
RUN go mod download

ARG LDFLAG_LOCATION=github.com/metal-toolbox/conditionorc/internal/version
ARG GIT_COMMIT
ARG GIT_BRANCH
ARG GIT_SUMMARY
ARG VERSION
ARG BUILD_DATE

COPY . ./

RUN GOOS=linux GOARCH=amd64 go build -o /usr/sbin/conditionorc \
-ldflags \
"-X ${LDFLAG_LOCATION}.GitCommit=${GIT_COMMIT} \
-X ${LDFLAG_LOCATION}.GitBranch=${GIT_BRANCH} \
-X ${LDFLAG_LOCATION}.GitSummary=${GIT_SUMMARY} \
-X ${LDFLAG_LOCATION}.AppVersion=${VERSION} \
-X ${LDFLAG_LOCATION}.BuildDate=${BUILD_DATE}"

FROM alpine:3.18.4
RUN apk -U add curl

COPY --from=build /usr/sbin/conditionorc /usr/sbin/

ENTRYPOINT ["/usr/sbin/conditionorc"]
