#/bin/bash

set -eux

docker rm $(docker ps -a -q) || true

docker net configure bridge
NETWORK=$(docker net create --driver=bridge)

CONTAINER=$(docker create ubuntu)
docker net plug $CONTAINER $NETWORK
docker start $CONTAINER
