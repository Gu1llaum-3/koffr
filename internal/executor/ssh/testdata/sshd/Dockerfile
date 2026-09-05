# A minimal SSH target for the executor contract suite.
#
# It is deliberately close to what a real database host offers: a shell, key
# authentication, TCP forwarding, and nothing else. AcceptEnv is wide open
# because the contract requires the environment to arrive, and Koffr passes it
# through the SSH protocol rather than through a command line, where it would be
# world-readable in /proc/<pid>/cmdline.
FROM alpine:3.20
RUN apk add --no-cache openssh-server \
 && ssh-keygen -A \
 && adduser -D -s /bin/sh probe \
 && sed -i 's/^probe:!:/probe:*:/' /etc/shadow \
 && mkdir -p /home/probe/.ssh \
 && chmod 700 /home/probe/.ssh
COPY authorized_keys /home/probe/.ssh/authorized_keys
RUN chown -R probe:probe /home/probe/.ssh \
 && chmod 600 /home/probe/.ssh/authorized_keys
RUN printf '%s\n' \
      'PermitRootLogin no' \
      'PasswordAuthentication no' \
      'AllowTcpForwarding yes' \
      'PermitOpen any' \
      'AcceptEnv *' \
      > /etc/ssh/sshd_config.d/probe.conf
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
