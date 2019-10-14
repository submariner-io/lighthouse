FROM registry.access.redhat.com/ubi8/ubi-minimal

WORKDIR /var/submariner

COPY lighthouse-controller.sh /usr/local/bin

RUN chmod +x /usr/local/bin/lighthouse-controller.sh

COPY lighthouse-controller /usr/local/bin

ENTRYPOINT lighthouse-controller.sh