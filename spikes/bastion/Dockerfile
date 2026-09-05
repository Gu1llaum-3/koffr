# Minimal SSH bastion: no shell tools, no database client. Its only job is to
# prove that Koffr can reach a database through a port-forward it opens itself.
#
# Note on the shadow edit below: `adduser -D` leaves the password field at '!',
# which sshd reads as a locked account and refuses even for public-key auth.
# '*' means "no password login" without locking the account.
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
# AllowTcpForwarding is the whole point; no shell session is needed.
RUN printf '%s\n' \
      'PermitRootLogin no' \
      'PasswordAuthentication no' \
      'AllowTcpForwarding yes' \
      'PermitOpen any' \
      'GatewayPorts no' \
      > /etc/ssh/sshd_config.d/probe.conf
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
