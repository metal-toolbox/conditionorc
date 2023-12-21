FROM alpine:3.19.0
RUN apk -U add curl

ENTRYPOINT ["/usr/sbin/conditionorc"]

COPY conditionorc /usr/sbin/conditionorc
RUN chmod +x /usr/sbin/conditionorc
