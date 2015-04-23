This document contains notes pertaining to Weavework's proof of concept implementation of the Container Network Model, using libcontainer and Jeff's plugin transport mechanism.

# How to build
    # mkdir -p $GOPATH/src/github.com/docker
    # cd $GOPATH/src/github.com/docker
    # git clone --branch network_extensions http://github.com/tomwilkie/docker
    # git clone --branch dev http://github.com/tomwilkie/libnetwork docker/vendor/src/github.com/docker/libnetwork
    # rm -rf docker/vendor/src/github.com/docker/libcontainer
    # git clone --branch existing_strategy http://github.com/tomwilkie/libcontainer docker/vendor/src/github.com/docker/libcontainer
    # mkdir docker/vendor/src/github.com/vishvananda
    # git clone http://github.com/vishvananda/netlink.git docker/vendor/src/github.com/vishvananda/netlink
    # cd docker
    # make
    # sudo ./bundles/1.7.0-plugins/binary/docker -dD

# 'Design' choices:
- choose verb plug and unplug (vs attach and detach) so we don't clash with container attach
- containers can have multiple endpoints on a given network implies unplug takes container id and endpoint id

# Next steps are:
- <s>boilerplate, internal datastructures</s>
- <s>find appropriate hook in points for plug</s>
- <s>docker run support</s>
- <s>find appropriate hook for unplug</s>
- <s>move code out of subdir to avoid circular imports</s>
- <s>make a plausible default/simple bridge driver</s>
- <s>persistence and tear down / setup</s>
- <s>transport to external plugins (lukestensions?)</s>
- <s>implement weave plugin.</s>
- make plug work for running containers
- drivers will want to specify a resolver, probably

# Little things
- <s>Make net create cli print id of network</s>
- <s>Make net plug print id of endpoint</s>
- Shorten network id in docker net list
- Show interfaces and network on docker inspect; show containers on docker net list

# Open Questions:
- <s>Should networks have ids and names (both unique)</s> Yes
- <s>Should endpoints have names?</s> No, just IDs
- libcontainer doesn't seem to have code to add veth pair to running container - should we add it there?
- Endpoints will need references to containers (ids/names at least); need to deal with circular references in json encoding


# Basic walkthrough:

# docker net create --driver simplebridge
67ea60624dfc1a6c08ba141c6cc022265e6fff81a44668c0135830a92de0b5e1
# docker net list
NETWORK ID                                                         NAME                DRIVER              LABELS
67ea60624dfc1a6c08ba141c6cc022265e6fff81a44668c0135830a92de0b5e1   stupefied_fermi     noop                {}
# docker create -i ubuntu /bin/bash
5e24637c4a1a8cfd2d5b44a1041496b9a599ba986349b636fd615126bd3e9a82
# docker ps -a
CONTAINER ID        IMAGE               COMMAND             CREATED             STATUS              PORTS               NAMES
5e24637c4a1a        ubuntu:latest       "/bin/bash"         4 seconds ago                                               backstabbing_hopper
# docker net plug backstabbing_hopper stupefied_fermi
5a624939c64c751c6276f852c2a01e54de49ba2894d922a07feebd2c56834900
# docker start -i backstabbing_hopper
>
> ifconfig -a
eth0      Link encap:Ethernet  HWaddr 02:42:0a:00:00:02
          inet addr:10.0.0.2  Bcast:0.0.0.0  Mask:255.255.0.0
          inet6 addr: fe80::42:aff:fe00:2/64 Scope:Link
          UP BROADCAST RUNNING  MTU:1500  Metric:1
          RX packets:16 errors:0 dropped:0 overruns:0 frame:0
          TX packets:8 errors:0 dropped:0 overruns:0 carrier:0
          collisions:0 txqueuelen:0
          RX bytes:1296 (1.2 KB)  TX bytes:648 (648.0 B)

eth1      Link encap:Ethernet  HWaddr 02:42:0a:03:00:01
          inet addr:10.0.0.6  Bcast:0.0.0.0  Mask:255.255.0.0
          inet6 addr: fe80::42:aff:fe03:1/64 Scope:Link
          UP BROADCAST RUNNING  MTU:1500  Metric:1
          RX packets:15 errors:0 dropped:0 overruns:0 frame:0
          TX packets:8 errors:0 dropped:0 overruns:0 carrier:0
          collisions:0 txqueuelen:0
          RX bytes:1206 (1.2 KB)  TX bytes:648 (648.0 B)

lo        Link encap:Local Loopback
          inet addr:127.0.0.1  Mask:255.0.0.0
          inet6 addr: ::1/128 Scope:Host
          UP LOOPBACK RUNNING  MTU:65536  Metric:1
          RX packets:0 errors:0 dropped:0 overruns:0 frame:0
          TX packets:0 errors:0 dropped:0 overruns:0 carrier:0
          collisions:0 txqueuelen:0
          RX bytes:0 (0.0 B)  TX bytes:0 (0.0 B)
