FROM alpine:latest

RUN apk add --no-cache openssh lsb-release && \
    ssh-keygen -A && \
    mkdir -p /root/.ssh && \
    chmod 700 /root/.ssh

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh", "sleep", "infinity"]
