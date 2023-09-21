FROM alpine:3.18.3
RUN apk -U add curl

ENTRYPOINT ["/usr/sbin/conditionorc"]

COPY conditionorc /usr/sbin/conditionorc
RUN chmod +x /usr/sbin/conditionorc
