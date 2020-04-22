FROM registry.access.redhat.com/ubi8/ubi-minimal

WORKDIR /var/submariner

COPY lighthouse-agent.sh /usr/local/bin

RUN chmod +x /usr/local/bin/lighthouse-agent.sh

COPY lighthouse-agent /usr/local/bin

ENTRYPOINT lighthouse-agent.sh