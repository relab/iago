FROM alpine:latest

RUN apk add --no-cache openssh lsb-release && \
    ssh-keygen -A && \
    mkdir -p /root/.ssh && \
    chmod 700 /root/.ssh && \
    printf '%s\n' \
        'HostKey /etc/ssh/ssh_host_rsa_key' \
        'HostKey /etc/ssh/ssh_host_ed25519_key' \
        'PermitRootLogin yes' \
        'AuthorizedKeysFile .ssh/authorized_keys' \
        'AllowTcpForwarding yes' \
        'Subsystem sftp internal-sftp' \
        > /etc/ssh/sshd_config

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh", "sleep", "infinity"]
