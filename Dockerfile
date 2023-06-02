FROM alpine:3.18.2

ENTRYPOINT ["/usr/sbin/conditionorc"]

COPY conditionorc /usr/sbin/conditionorc
RUN chmod +x /usr/sbin/conditionorc
